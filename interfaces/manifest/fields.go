package manifest

func Choice(name, title, value string, choices []string, restart bool) ConfigField {
	field := Text(name, title, value, false, restart)
	field.Choices = append([]string(nil), choices...)
	return field
}

func FormattedText(name, title, value, format string, secret, restart bool) ConfigField {
	field := Text(name, title, value, secret, restart)
	field.Format = format
	return field
}

func Text(name, title, value string, secret, restart bool) ConfigField {
	return ConfigField{Name: name, Title: title, Type: String, Default: value, Secret: secret, RestartRequired: restart}
}

func Number(name, title string, value, minimum, maximum int64, restart bool) ConfigField {
	return ConfigField{Name: name, Title: title, Type: Int, Default: value, Min: &minimum, Max: &maximum, RestartRequired: restart}
}

func Flag(name, title string, value, restart bool) ConfigField {
	return ConfigField{Name: name, Title: title, Type: Bool, Default: value, RestartRequired: restart}
}

func List(name, title string, restart bool) ConfigField {
	return ConfigField{Name: name, Title: title, Type: Strings, Default: []string{}, RestartRequired: restart}
}
