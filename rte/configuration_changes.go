package rte

import (
	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/rte/config"
)

func configurationChanges(fields []manifest.ConfigField, before, after config.View) []ConfigurationChange {
	changes := []ConfigurationChange{}
	for _, field := range fields {
		if before.EqualFields(after, field.Name) {
			continue
		}
		change := ConfigurationChange{Name: field.Name, Secret: field.Secret}
		_ = before.Get(field.Name, &change.Before)
		_ = after.Get(field.Name, &change.After)
		if field.Secret {
			change.Before = secretPresence(change.Before)
			change.After = secretPresence(change.After)
		}
		changes = append(changes, change)
	}
	return changes
}

func secretPresence(value any) string {
	text, _ := value.(string)
	if text == "" {
		return "未设置"
	}
	return "已设置（不回显）"
}
