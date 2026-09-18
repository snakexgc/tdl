package rte_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func TestComponentPagesValidateLocalPathsAndOwnSnapshots(t *testing.T) {
	for _, path := range []string{"https://example.com/", "//example.com/", "javascript:alert(1)", "relative", "/\\example.com"} {
		r := rte.NewRegistry()
		require.Error(t, r.Register(manifest.Manifest{ID: providerID, Pages: []manifest.Page{{Path: path, Title: "Page"}}}, func() rte.Component { return &component{} }))
	}
	r := rte.NewRegistry()
	pages := []manifest.Page{{Path: "/feature", Title: "Feature"}}
	require.NoError(t, r.Register(manifest.Manifest{ID: providerID, Pages: pages}, func() rte.Component { return &component{} }))
	pages[0].Title = "mutated"
	host, err := r.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	entries := host.Configurations()
	require.Equal(t, "Feature", entries[0].Pages[0].Title)
	entries[0].Pages[0].Title = "changed response"
	require.Equal(t, "Feature", host.Configurations()[0].Pages[0].Title)
}
