package rte

import (
	"encoding/json"

	"github.com/snakexgc/tdl/interfaces/manifest"
)

// Configuration describes a running component for a schema-driven HMI.
// Sensitive values are omitted; callers never receive references to live data.
type Configuration struct {
	Feature        manifest.Feature       `json:"feature"`
	Revision       string                 `json:"revision,omitempty"`
	PendingRestart bool                   `json:"pending_restart,omitempty"`
	Scope          Scope                  `json:"scope,omitempty"`
	Enabled        bool                   `json:"enabled"`
	Error          string                 `json:"error,omitempty"`
	ID             string                 `json:"id"`
	Title          string                 `json:"title"`
	State          State                  `json:"state"`
	Fields         []manifest.ConfigField `json:"fields"`
	Values         map[string]any         `json:"values"`
	Pages          []manifest.Page        `json:"pages"`
}

func (r *Runtime) Configurations() []Configuration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configurationsLocked()
}

func (r *Runtime) configurationsLocked() []Configuration {
	result := make([]Configuration, 0, len(r.order))
	for _, id := range r.order {
		item := r.instances[id]
		m := item.registration.Manifest
		entry := Configuration{ID: id, Title: m.Title, State: item.status.State, Enabled: true, Fields: []manifest.ConfigField{}, Values: map[string]any{}}
		entry.Feature = m.Feature
		entry.Pages = append([]manifest.Page{}, m.Pages...)
		for i := range entry.Pages {
			entry.Pages[i].Settings = append([]string(nil), entry.Pages[i].Settings...)
		}
		for _, field := range m.Config {
			if field.Secret {
				field.Default = ""
			} else {
				var value any
				if item.config.Get(field.Name, &value) == nil {
					entry.Values[field.Name] = value
				}
			}
			// Schema fields may contain pointer bounds or slice defaults.
			data, err := json.Marshal(field)
			if err != nil {
				continue
			}
			var copied manifest.ConfigField
			if json.Unmarshal(data, &copied) == nil {
				entry.Fields = append(entry.Fields, copied)
			}
		}
		result = append(result, entry)
	}
	return result
}
