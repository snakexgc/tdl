package rte

import (
	"encoding/json"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/rte/config"
)

// Configuration describes a running component for a schema-driven HMI.
// Sensitive values are omitted; callers never receive references to live data.
type Configuration struct {
	ActiveEnabled  *bool                  `json:"active_enabled,omitempty"`
	Changes        []ConfigurationChange  `json:"changes,omitempty"`
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
	Previews       map[string]string      `json:"previews,omitempty"`
	Pages          []manifest.Page        `json:"pages"`
}

// ConfigurationChange compares the startup value with the value saved for the
// next start. Secret values are represented only by their presence.
type ConfigurationChange struct {
	Name   string `json:"name"`
	Before any    `json:"before"`
	After  any    `json:"after"`
	Secret bool   `json:"secret,omitempty"`
}

func (r *Runtime) Configurations() []Configuration {
	result, _ := r.configurationSnapshots()
	return result
}

// Configuration and state come from the same publication; lifecycle hooks and
// runnable drains must not hold up the management plane's reads.
func (r *Runtime) configurationSnapshots() ([]Configuration, map[string]config.View) {
	items := *r.observed.Load()
	result := make([]Configuration, 0, len(items))
	views := make(map[string]config.View, len(items))
	for _, component := range items {
		item := component.observation.Load()
		m := component.registration.Manifest
		id := m.ID
		views[id] = item.config
		entry := Configuration{ID: id, Title: m.Title, State: item.status.State, Enabled: true, Fields: []manifest.ConfigField{}, Values: map[string]any{}}
		entry.Feature = m.Feature
		entry.Previews = publicPreviews(m.Config, item.config)
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
	return result, views
}
