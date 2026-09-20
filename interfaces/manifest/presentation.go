package manifest

// WithSettings supplies presentation defaults without changing configuration
// keys, validation or ownership. Individual fields may override the category.
func WithSettings(m Manifest, tab, section string, advanced ...string) Manifest {
	for i := range m.Config {
		f := &m.Config[i]
		if f.SettingsTab == "" {
			f.SettingsTab = tab
		}
		if f.SettingsSection == "" {
			f.SettingsSection = section
		}
		for _, name := range advanced {
			if f.Name == name {
				f.Advanced = true
			}
		}
	}
	return m
}

func (f ConfigField) InSettings(tab, section string) ConfigField {
	f.SettingsTab, f.SettingsSection = tab, section
	return f
}

func (f ConfigField) WithHelp(help string) ConfigField {
	f.Help = help
	return f
}

func (m Manifest) SettingsOrder(order int) Manifest {
	for i := range m.Config {
		m.Config[i].SettingsOrder = order
	}
	return m
}
