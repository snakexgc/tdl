package configuration

import "maps"

// NTP server preferences are no longer supported. Drop only the known retired
// fields from an owned copy, without importing their values into the pool or
// modifying caller data. The next explicit save persists the cleaned document.
func (s *Service) discardRetiredTimePreferences(input Document) Document {
	retired := map[string]string{"account.telegram": "ntp", "time.sync": "server"}
	cloned := false
	for _, definition := range s.catalog.Definitions() {
		id := definition.Manifest.ID
		field, known := retired[id]
		if !known {
			continue
		}
		component := input.Components[id]
		if _, exists := component.Values[field]; !exists {
			continue
		}
		if !cloned {
			input.Components = maps.Clone(input.Components)
			cloned = true
		}
		component.Values = maps.Clone(component.Values)
		delete(component.Values, field)
		input.Components[id] = component
	}
	return input
}
