// Package rte assembles statically registered components. It does not import
// application implementations or own Telegram connections and task storage.
package rte

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/snakexgc/tdl/bsw/services/dem"
	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/eventbus"
	"github.com/snakexgc/tdl/rte/schedule"
)

type Component interface {
	Init(context.Context, Kernel) error
	Start(context.Context) error
	// Stop may be retried after an error; retain resources needed to finish cleanup.
	Stop(context.Context) error
	// Reconfigure must leave the old configuration intact on error.
	Reconfigure(context.Context, config.View) error
}

type Kernel struct {
	Account   types.AccountID
	Config    config.View
	resolve   func(string) (any, error)
	provide   func(string, any) error
	Events    Events
	Runnables *schedule.Group
}

func (k Kernel) Resolve(name string) (any, error)     { return k.resolve(name) }
func (k Kernel) Provide(name string, value any) error { return k.provide(name, value) }

type (
	Factory      func() Component
	Registration struct {
		Manifest manifest.Manifest
		New      Factory
	}
)
type Registry struct{ entries map[string]Registration }

func NewRegistry() *Registry { return &Registry{entries: make(map[string]Registration)} }

func (r *Registry) Register(m manifest.Manifest, factory Factory) error {
	if m.ID == "" || factory == nil {
		return errors.New("component ID and factory are required")
	}
	if _, exists := r.entries[m.ID]; exists {
		return fmt.Errorf("duplicate component %q", m.ID)
	}
	// Keep registration metadata independent of the caller's slices.
	m.Provides = append([]manifest.Port(nil), m.Provides...)
	m.Requires = append([]manifest.Require(nil), m.Requires...)
	m.Config = append([]manifest.ConfigField(nil), m.Config...)
	for i := range m.Config {
		field := &m.Config[i]
		field.Choices = append([]string(nil), field.Choices...)
		if field.Min != nil {
			minimum := *field.Min
			field.Min = &minimum
		}
		if field.Max != nil {
			maximum := *field.Max
			field.Max = &maximum
		}
		data, err := json.Marshal(field.Default)
		if err != nil {
			return fmt.Errorf("%s.%s default: %w", m.ID, field.Name, err)
		}
		field.Default = json.RawMessage(data)
	}
	m.Publishes = append([]string(nil), m.Publishes...)
	m.Subscribes = append([]string(nil), m.Subscribes...)
	m.Pages = append([]manifest.Page(nil), m.Pages...)
	for i := range m.Pages {
		m.Pages[i].Settings = append([]string(nil), m.Pages[i].Settings...)
	}
	m.Commands = append([]types.ConsoleCommand(nil), m.Commands...)
	for i := range m.Commands {
		m.Commands[i].Aliases = append([]string(nil), m.Commands[i].Aliases...)
	}
	seenPages := map[string]bool{}
	for _, page := range m.Pages {
		parsed, err := url.ParseRequestURI(page.Path)
		if err != nil || !strings.HasPrefix(page.Path, "/") || strings.HasPrefix(page.Path, "//") || strings.Contains(page.Path, "\\") || parsed.Host != "" || parsed.Scheme != "" || page.Title == "" || seenPages[page.Path] {
			return fmt.Errorf("%s: invalid or duplicate component page", m.ID)
		}
		seenPages[page.Path] = true
	}
	r.entries[m.ID] = Registration{m, factory}
	return nil
}

type State string

const (
	Starting State = "starting"
	Stopping State = "stopping"
	Running  State = "running"
	Failed   State = "failed"
	Blocked  State = "blocked"
	Stopped  State = "stopped"
)

type Status struct {
	ID     string `json:"id"`
	State  State  `json:"state"`
	Detail string `json:"detail,omitempty"`
}
type instance struct {
	registration Registration
	component    Component
	config       config.View
	status       Status
	initialized  bool
	cancel       context.CancelFunc
	owner        <-chan struct{}
	runnables    *schedule.Group
	observation  atomic.Pointer[observation]
}

type observation struct {
	status    Status
	runnables *schedule.Group
}

// Called under Runtime.mu; health readers only access the immutable snapshot.
func (i *instance) setStatus(status Status) {
	i.status = status
	i.observation.Store(&observation{status: status, runnables: i.runnables})
}

type Runtime struct {
	mu          sync.Mutex
	registry    *Registry
	context     context.Context
	order       []string
	instances   map[string]*instance
	observed    atomic.Pointer[[]*instance]
	providers   map[string]string
	ports       map[string]any
	account     types.AccountID
	started     bool
	stopped     bool
	stopping    bool
	bus         *eventbus.Bus
	diagnostics *dem.Store
}

// Build validates the complete graph and all configurations before invoking
// any factories. A nil enabled map enables all registered components.
func (r *Registry) Build(account types.AccountID, enabled map[string]bool, values map[string]map[string]any) (*Runtime, error) {
	run := &Runtime{account: account, instances: map[string]*instance{}, providers: map[string]string{}, ports: map[string]any{}, bus: eventbus.New(), diagnostics: dem.New(account, 128)}
	run.registry = NewRegistry()
	for id, registration := range r.entries {
		run.registry.entries[id] = registration
	}
	if account == "" {
		return nil, errors.New("account is required")
	}
	for id := range enabled {
		if _, ok := r.entries[id]; !ok {
			return nil, fmt.Errorf("unknown component %q", id)
		}
	}
	for id := range values {
		if _, ok := r.entries[id]; !ok {
			return nil, fmt.Errorf("unknown component config %q", id)
		}
	}
	var ids []string
	for id := range r.entries {
		if enabled == nil || enabled[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	provided := map[string]manifest.Port{}
	for _, id := range ids {
		entry := r.entries[id]
		view, err := config.New(entry.Manifest.Config, values[id])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		run.instances[id] = &instance{registration: entry, config: view, status: Status{ID: id, State: Stopped}}
		for _, p := range entry.Manifest.Provides {
			if err := validatePort(p); err != nil {
				return nil, fmt.Errorf("%s: %w", id, err)
			}
			if previous, exists := run.providers[p.Name]; exists {
				return nil, fmt.Errorf("port %s provided by both %s and %s", p.Name, previous, id)
			}
			run.providers[p.Name], provided[p.Name] = id, p
		}
	}
	deps := map[string][]string{}
	for _, id := range ids {
		seen := map[string]bool{}
		for _, req := range r.entries[id].Manifest.Requires {
			if err := validatePort(req.Port); err != nil {
				return nil, fmt.Errorf("%s: %w", id, err)
			}
			if seen[req.Name] {
				return nil, fmt.Errorf("%s: duplicate required port %s", id, req.Name)
			}
			seen[req.Name] = true
			provider, exists := run.providers[req.Name]
			if !exists {
				if req.Optional {
					continue
				}
				return nil, fmt.Errorf("%s requires port %s v%d.%d: no enabled provider", id, req.Name, req.Major, req.Minor)
			}
			p := provided[req.Name]
			if p.Type != req.Type || p.Major != req.Major || p.Minor < req.Minor {
				return nil, fmt.Errorf("%s: incompatible port %s from %s", id, req.Name, provider)
			}
			deps[id] = append(deps[id], provider)
		}
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("component dependency cycle at %s", id)
		}
		if done[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range deps[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[id], done[id] = false, true
		run.order = append(run.order, id)
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	for _, item := range run.instances {
		item.setStatus(item.status)
	}
	run.publishInstances()
	return run, nil
}

// Publish membership separately from per-component status. Health must not
// traverse maps being replaced by reconciliation or wait for lifecycle hooks.
func (r *Runtime) publishInstances() {
	items := make([]*instance, 0, len(r.order))
	for _, id := range r.order {
		items = append(items, r.instances[id])
	}
	r.observed.Store(&items)
}

func validatePort(p manifest.Port) error {
	if p.Name == "" || p.Major < 1 || p.Minor < 0 || p.Type == nil || p.Type.Kind() != reflect.Interface {
		return fmt.Errorf("invalid port declaration %q", p.Name)
	}
	return nil
}

// Start isolates lifecycle errors. Required dependants are blocked; unrelated
// components still start. Components must return promptly and honor ctx.
func (r *Runtime) Start(ctx context.Context) []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.stopped || r.stopping {
		return r.statuses()
	}
	return r.startLocked(ctx)
}

func (r *Runtime) startLocked(ctx context.Context) []Status {
	if !r.started {
		r.context = ctx
	}
	ctx = r.context
	r.started = true
	for _, id := range r.order {
		item := r.instances[id]
		if item.status.State == Running {
			continue
		}
		for _, req := range item.registration.Manifest.Requires {
			provider := r.providers[req.Name]
			if !req.Optional && r.instances[provider].status.State != Running {
				item.setStatus(Status{id, Blocked, "dependency unavailable: " + provider})
				break
			}
		}
		if item.status.State == Blocked {
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		item.cancel, item.owner = cancel, runCtx.Done()
		kernel := Kernel{Account: r.account, Config: item.config}
		item.runnables = schedule.NewObserved(runCtx, func(name string, err error) { r.diagnostics.Report(id, "runnable:"+name, err) })
		item.setStatus(Status{ID: id, State: Starting})
		kernel.Runnables = item.runnables
		kernel.Events = Events{bus: r.bus, account: r.account, ctx: runCtx, publishes: item.registration.Manifest.Publishes, subscribes: item.registration.Manifest.Subscribes, report: func(topic string, err error) { r.diagnostics.Report(id, "event:"+topic, err) }}
		// Required ports are a private immutable snapshot; resolving them from
		// component goroutines never touches the runtime's mutable binding map.
		required := make(map[string]any)
		for _, req := range item.registration.Manifest.Requires {
			required[req.Name] = r.ports[req.Name]
		}
		var bindMu sync.Mutex
		bindings := make(map[string]any)
		sealed := false
		kernel.resolve = func(name string) (any, error) {
			for _, req := range item.registration.Manifest.Requires {
				if req.Name == name {
					value := required[name]
					if value != nil {
						return value, nil
					}
					return nil, fmt.Errorf("port %s unavailable", name)
				}
			}
			return nil, fmt.Errorf("%s: undeclared required port %s", id, name)
		}
		kernel.provide = func(name string, value any) error {
			bindMu.Lock()
			defer bindMu.Unlock()
			if sealed {
				return errors.New("ports may only be bound during Init")
			}
			for _, p := range item.registration.Manifest.Provides {
				if p.Name != name {
					continue
				}
				if isNil(value) || !reflect.TypeOf(value).Implements(p.Type) {
					return fmt.Errorf("%s: invalid implementation for %s", id, name)
				}
				if _, exists := bindings[name]; exists {
					return fmt.Errorf("port %s already bound", name)
				}
				bindings[name] = value
				return nil
			}
			return fmt.Errorf("%s: undeclared provided port %s", id, name)
		}
		err := invoke(func() error {
			item.component = item.registration.New()
			if isNil(item.component) {
				return errors.New("factory returned nil")
			}
			item.initialized = true
			if err := item.component.Init(runCtx, kernel); err != nil {
				return err
			}
			bindMu.Lock()
			sealed = true
			for name, value := range bindings {
				r.ports[name] = value
			}
			bindMu.Unlock()
			for _, p := range item.registration.Manifest.Provides {
				if _, ok := r.ports[p.Name]; !ok {
					return fmt.Errorf("port %s was not bound", p.Name)
				}
			}
			return item.component.Start(runCtx)
		})
		bindMu.Lock()
		sealed = true
		bindMu.Unlock()
		if err != nil {
			cancel()
			drainErr := errors.Join(r.bus.WaitScope(ctx, runCtx.Done()), item.runnables.Stop(ctx))
			err = errors.Join(err, drainErr)
			if item.initialized && drainErr == nil {
				stopErr := invoke(func() error { return item.component.Stop(ctx) })
				err = errors.Join(err, stopErr)
				item.initialized = stopErr != nil
			}
			for _, p := range item.registration.Manifest.Provides {
				delete(r.ports, p.Name)
			}
			item.setStatus(Status{id, Failed, err.Error()})
			r.diagnostics.Report(id, "start", err)
		} else {
			item.setStatus(Status{id, Running, ""})
		}
	}
	return r.statuses()
}

// Resolve is the composition root's port lookup. SWCs receive scoped Kernel
// lookups, which reject access to undeclared requirements.
func (r *Runtime) Resolve(name string) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolvePortLocked(r.providers[name], name)
}

// Resolve identity, state and value together so reconciliation cannot substitute
// another component between the ownership check and the port lookup.
func (r *Runtime) resolveComponentPort(id, name string) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolvePortLocked(id, name)
}

func (r *Runtime) resolvePortLocked(id, name string) (any, error) {
	item := r.instances[id]
	if item != nil && item.status.State == Running && r.providers[name] == id {
		if value, ok := r.ports[name]; ok {
			return value, nil
		}
	}
	return nil, fmt.Errorf("port %s unavailable", name)
}

func (r *Runtime) Reconfigure(ctx context.Context, id string, values map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	item, ok := r.instances[id]
	if !ok {
		return fmt.Errorf("unknown component %s", id)
	}
	view, err := config.New(item.registration.Manifest.Config, values)
	if err != nil {
		return err
	}
	if item.status.State != Running {
		return fmt.Errorf("component %s is not running", id)
	}
	if item.config.Equal(view) {
		return nil
	}
	if err := invoke(func() error { return item.component.Reconfigure(ctx, view) }); err != nil {
		return err
	}
	item.config = view
	return nil
}

// Stop cancels work, drains it, then releases resources in reverse dependency
// order. An incomplete drain or cleanup preserves resources for a later retry.
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return nil
	}
	r.stopping = true
	// Stop event handlers before releasing component resources. All component
	// contexts are canceled first, so a handler waiting on another component can
	// observe shutdown too.
	for _, item := range r.instances {
		if item.initialized {
			item.setStatus(Status{ID: item.status.ID, State: Stopping})
		}
		if item.cancel != nil {
			item.cancel()
		}
	}
	if err := r.bus.Close(ctx); err != nil {
		for _, item := range r.instances {
			if item.initialized {
				item.setStatus(Status{item.status.ID, Failed, err.Error()})
				r.diagnostics.Report(item.status.ID, "stop:events", err)
			}
		}
		return err
	}
	// Drain every runnable before releasing any component: even an unrelated
	// worker can still hold a port to a provider that is earlier in stop order.
	for _, id := range r.order {
		item := r.instances[id]
		if item.runnables != nil {
			if err := item.runnables.Stop(ctx); err != nil {
				item.setStatus(Status{id, Failed, err.Error()})
				r.diagnostics.Report(id, "stop:runnables", err)
				return fmt.Errorf("%s: %w", id, err)
			}
		}
	}
	for i := len(r.order) - 1; i >= 0; i-- {
		item := r.instances[r.order[i]]
		if !item.initialized {
			item.setStatus(Status{ID: r.order[i], State: Stopped})
			continue
		}
		err := invoke(func() error { return item.component.Stop(ctx) })
		if err != nil {
			item.setStatus(Status{r.order[i], Failed, err.Error()})
			r.diagnostics.Report(r.order[i], "stop", err)
			return fmt.Errorf("%s: %w", r.order[i], err)
		}
		item.initialized = false
		item.setStatus(Status{ID: r.order[i], State: Stopped})
		for _, p := range item.registration.Manifest.Provides {
			delete(r.ports, p.Name)
		}
	}
	clear(r.ports)
	r.stopped = true
	return nil
}

func (r *Runtime) Statuses() []Status { r.mu.Lock(); defer r.mu.Unlock(); return r.statuses() }
func (r *Runtime) statuses() []Status {
	result := make([]Status, 0, len(r.order))
	for _, id := range r.order {
		result = append(result, r.instances[id].status)
	}
	return result
}

func invoke(fn func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("component panic: %v", p)
		}
	}()
	return fn()
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
