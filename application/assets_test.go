package application

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComponentAssetsKeepPublicPathsAndUniqueOwnership(t *testing.T) {
	assets := WebAssets()
	seen := map[string]bool{}
	for _, source := range assets.(assetSources) {
		require.NoError(t, fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			require.False(t, seen[path], "duplicate asset ownership: %s", path)
			seen[path] = true
			want, err := fs.ReadFile(source, path)
			require.NoError(t, err)
			got, err := fs.ReadFile(assets, path)
			require.NoError(t, err)
			require.Equal(t, want, got)
			return nil
		}))
	}
	for _, path := range []string{"index.html", "aria2ng.html", "views/update.html", "views/user.html", "views/downloads.html", "views/forwards.html", "static/js/update.js"} {
		require.True(t, seen[path], path)
	}
	_, err := assets.Open("../config.json")
	require.ErrorIs(t, err, fs.ErrInvalid)
}

func TestComponentRouteOwnershipPreservesAuthentication(t *testing.T) {
	seen := map[string]bool{}
	public := map[string]bool{"/login": true, "/api/auth/session": true, "/api/auth/login": true}
	for _, route := range WebRoutes() {
		require.False(t, seen[route.Path], "duplicate route: %s", route.Path)
		seen[route.Path] = true
		require.Equal(t, public[route.Path], route.Public, route.Path)
	}
	for _, path := range []string{"/api/user", "/api/update/apply", "/api/forwards/actions", "/aria2/jsonrpc", "/api/download-tasks/actions", "/api/components"} {
		require.True(t, seen[path], path)
	}
}

func TestComponentPagesDeclareTheirOwnLoaders(t *testing.T) {
	catalog, err := Catalog()
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, definition := range catalog.Definitions() {
		for _, page := range definition.Manifest.Pages {
			require.False(t, seen[page.Path], "duplicate page: %s", page.Path)
			seen[page.Path] = true
			if page.View == "" {
				continue
			}
			for _, path := range []string{"views/" + page.View + ".html", page.Module[1:], page.Style[1:]} {
				_, err := fs.ReadFile(definition.Assets, path)
				require.NoError(t, err, "%s must own %s", definition.Manifest.ID, path)
			}
			data, err := fs.ReadFile(definition.Assets, page.Module[1:])
			require.NoError(t, err)
			require.Contains(t, string(data), "export const page =")
		}
	}
	for _, path := range []string{"/user", "/downloads", "/forwards", "/config", "/modules", "/kv", "/update", "/dashboard"} {
		require.True(t, seen[path], path)
	}
}
