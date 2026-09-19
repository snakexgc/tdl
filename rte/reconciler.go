package rte

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/snakexgc/tdl/interfaces/types"
)

// ManagedUnit binds a resource owner to the generic production lifecycle graph.
// Revision contains only settings that require replacing that resource owner.
type ManagedUnit struct {
	ID       string
	Requires []string
	Enabled  bool
	Revision string
	Running  func() bool
	Update   func(context.Context) error
	Start    func(context.Context) error
	Stop     func(context.Context) error
}

type managedInstance struct {
	unit        ManagedUnit
	revision    string
	active      bool
	pendingStop bool
}

type Reconciler struct {
	mu        sync.Mutex
	account   types.AccountID
	instances map[string]*managedInstance
	snapshot  atomic.Pointer[Health]
}

func NewReconciler(account types.AccountID) *Reconciler {
	r := &Reconciler{account: account, instances: map[string]*managedInstance{}}
	r.snapshot.Store(&Health{Account: account, Components: []ComponentHealth{}})
	return r
}

func (r *Reconciler) Health() Health {
	snapshot := *r.snapshot.Load()
	snapshot.Components = append([]ComponentHealth{}, snapshot.Components...)
	return snapshot
}

func orderUnits(units map[string]ManagedUnit) ([]string, error) {
	names := make([]string, 0, len(units))
	for id := range units {
		names = append(names, id)
	}
	sort.Strings(names)
	visiting, visited := map[string]bool{}, map[string]bool{}
	order := []string{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("resource dependency cycle at %s", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range units[id].Requires {
			if _, ok := units[dependency]; !ok {
				return fmt.Errorf("%s: unknown resource dependency %s", id, dependency)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		order = append(order, id)
		return nil
	}
	for _, id := range names {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// Reconcile validates before changing resources, stops consumers first, and
// preserves an owner after a failed stop. Failures do not prevent unrelated starts.
func (r *Reconciler) Reconcile(ctx context.Context, desired []ManagedUnit) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	units := map[string]ManagedUnit{}
	for _, unit := range desired {
		if unit.ID == "" || unit.Start == nil || unit.Stop == nil {
			return errors.New("resource requires ID, start and stop")
		}
		if _, ok := units[unit.ID]; ok {
			return fmt.Errorf("duplicate resource %s", unit.ID)
		}
		unit.Requires = append([]string{}, unit.Requires...)
		units[unit.ID] = unit
	}
	order, err := orderUnits(units)
	if err != nil {
		return err
	}
	// A removed resource remains in the stop graph until it has actually exited.
	for id, previous := range r.instances {
		if _, ok := units[id]; !ok {
			unit := previous.unit
			unit.Enabled = false
			units[id] = unit
		}
	}
	order, err = orderUnits(units)
	if err != nil {
		return err
	}
	stopUnits := make(map[string]ManagedUnit, len(units))
	for id, unit := range units {
		if old := r.instances[id]; old != nil && old.active {
			unit = old.unit
		}
		stopUnits[id] = unit
	}
	stopOrder, err := orderUnits(stopUnits)
	if err != nil {
		return err
	}
	statuses := map[string]Status{}
	publish := func(id string, state State, detail string) {
		statuses[id] = Status{ID: id, State: state, Detail: detail}
		health := Health{Account: r.account, Components: []ComponentHealth{}}
		for _, key := range order {
			if status, ok := statuses[key]; ok {
				health.Components = append(health.Components, ComponentHealth{Status: status})
			}
		}
		r.snapshot.Store(&health)
	}
	restart := map[string]bool{}
	for _, id := range order {
		unit := units[id]
		old := r.instances[id]
		if old == nil {
			old = &managedInstance{unit: unit, revision: unit.Revision}
			r.instances[id] = old
		}
		if !old.pendingStop {
			observe := unit.Running
			if old.active {
				observe = old.unit.Running
			}
			if observe != nil {
				running := observe()
				if old.active && !running {
					// An exited process still owns resources and consumers may
					// retain its ports. Drain the old graph before replacing it.
					old.pendingStop = true
				} else {
					old.active = running
				}
			}
		}
		restart[id] = !unit.Enabled || old.pendingStop || (old.active && (old.revision != unit.Revision || !slices.Equal(old.unit.Requires, unit.Requires)))
		for _, dependency := range unit.Requires {
			restart[id] = restart[id] || restart[dependency]
		}
		state := Stopped
		if old.active {
			state = Running
		}
		publish(id, state, "")
	}
	for _, id := range stopOrder {
		for _, dependency := range stopUnits[id].Requires {
			restart[id] = restart[id] || restart[dependency]
		}
	}
	var combined error
	stopFailed := map[string]bool{}
	for i := len(stopOrder) - 1; i >= 0; i-- {
		id := stopOrder[i]
		old := r.instances[id]
		if !old.active || !restart[id] {
			continue
		}
		blocked := false
		for consumer, unit := range stopUnits {
			for _, dependency := range unit.Requires {
				if dependency == id && stopFailed[consumer] {
					blocked = true
				}
			}
		}
		if blocked {
			stopFailed[id] = true
			publish(id, Blocked, "dependent resource has not stopped")
			continue
		}
		publish(id, Stopping, "")
		if err := invoke(func() error { return old.unit.Stop(ctx) }); err != nil {
			stopFailed[id] = true
			old.pendingStop = true
			combined = errors.Join(combined, fmt.Errorf("stop %s: %w", id, err))
			publish(id, Stopping, err.Error())
			continue
		}
		old.active, old.pendingStop = false, false
		publish(id, Stopped, "")
	}
	for _, id := range order {
		unit := units[id]
		old := r.instances[id]
		if stopFailed[id] {
			continue
		}
		if !unit.Enabled {
			publish(id, Stopped, "disabled")
			old.unit = unit
			continue
		}
		blocked := ""
		for _, dependency := range unit.Requires {
			if statuses[dependency].State != Running {
				blocked = "requires " + dependency
				break
			}
		}
		if blocked != "" {
			publish(id, Blocked, blocked)
			continue
		}
		if err := ctx.Err(); err != nil {
			combined = errors.Join(combined, err)
			publish(id, Blocked, err.Error())
			continue
		}
		if unit.Update != nil {
			if err := invoke(func() error { return unit.Update(ctx) }); err != nil {
				combined = errors.Join(combined, err)
				publish(id, Failed, err.Error())
				continue
			}
		}
		if !old.active {
			publish(id, Starting, "")
			old.unit, old.active, old.pendingStop = unit, true, true
			if err := invoke(func() error { return unit.Start(ctx) }); err != nil {
				combined = errors.Join(combined, fmt.Errorf("start %s: %w", id, err))
				if cleanupErr := invoke(func() error { return unit.Stop(ctx) }); cleanupErr != nil {
					combined = errors.Join(combined, fmt.Errorf("cleanup %s: %w", id, cleanupErr))
					publish(id, Stopping, errors.Join(err, cleanupErr).Error())
				} else {
					old.active, old.pendingStop = false, false
					publish(id, Failed, err.Error())
				}
				continue
			}
			old.active, old.pendingStop = true, false
		}
		old.unit, old.revision = unit, unit.Revision
		publish(id, Running, "")
	}
	return combined
}
