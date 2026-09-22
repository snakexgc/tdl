package configuration

import "maps"

// Accept version-1 files from before time synchronization became a process SWC.
// This is an in-memory, copy-on-write migration; an ordinary explicit save will
// persist the canonical form. Startup does not rewrite the user's file.
func (s *Service) migrateTimeConfiguration(input Document) Document {
	const accountID, clockID = "account.telegram", "time.sync"
	account := input.Components[accountID]
	legacy, exists := account.Values["ntp"]
	if !exists {
		return input
	}
	known := false
	for _, definition := range s.catalog.Definitions() {
		known = known || definition.Manifest.ID == clockID
	}
	if !known {
		return input
	}
	input.Components = maps.Clone(input.Components)
	account.Values = maps.Clone(account.Values)
	delete(account.Values, "ntp")
	input.Components[accountID] = account
	clock, exists := input.Components[clockID]
	if !exists {
		clock.Enabled = true
	}
	clock.Values = maps.Clone(clock.Values)
	if clock.Values == nil {
		clock.Values = map[string]any{}
	}
	if _, explicit := clock.Values["server"]; !explicit {
		clock.Values["server"] = legacy
	}
	input.Components[clockID] = clock
	return input
}
