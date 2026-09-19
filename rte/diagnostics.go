package rte

import (
	"sort"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/schedule"
)

type ComponentHealth struct {
	Status
	Runnables []schedule.Status `json:"runnables"`
}

type Health struct {
	Account    types.AccountID         `json:"account"`
	Components []ComponentHealth       `json:"components"`
	Events     []types.DiagnosticEvent `json:"events"`
}

// Health reads independently published lifecycle snapshots. It must remain
// available while a lifecycle hook is blocked under the runtime's mutation lock.
func (r *Runtime) Health() Health {
	result := Health{Account: r.account, Events: r.diagnostics.Events(), Components: []ComponentHealth{}}
	for _, component := range *r.observed.Load() {
		item := component.observation.Load()
		health := ComponentHealth{Status: item.status, Runnables: []schedule.Status{}}
		if item.runnables != nil {
			health.Runnables = item.runnables.Statuses()
		}
		sort.Slice(health.Runnables, func(i, j int) bool { return health.Runnables[i].Name < health.Runnables[j].Name })
		result.Components = append(result.Components, health)
	}
	return result
}
