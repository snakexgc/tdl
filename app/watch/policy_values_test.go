package watch

const (
	filterComponentID = "filter.rules"
	namingComponentID = "naming.rules"
)

func namingConfig(opts Options) map[string]any {
	maximum := opts.FilenameMaxLength
	if maximum <= 0 || maximum > 255 {
		maximum = 255
	}
	return map[string]any{"filename": opts.Template, "directory": opts.Dir, "max_bytes": maximum}
}

func filterConfig(opts Options) map[string]any {
	include, exclude := opts.Include, opts.Exclude
	if include == nil {
		include = []string{}
	}
	if exclude == nil {
		exclude = []string{}
	}
	return map[string]any{"include": include, "exclude": exclude, "min_mb": opts.FileSizeMinMB, "max_mb": opts.FileSizeMaxMB}
}

// policyValues builds explicit test policies.
func policyValues(opts Options) map[string]map[string]any {
	return map[string]map[string]any{
		filterComponentID: filterConfig(opts),
		namingComponentID: namingConfig(opts),
	}
}
