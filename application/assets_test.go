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
