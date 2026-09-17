package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
)

const (
	countField    = "count"
	namesField    = "names"
	originalValue = "original"
)

func TestViewValidationAndOwnership(t *testing.T) {
	zero := int64(0)
	schema := []manifest.ConfigField{{Name: namesField, Type: manifest.Strings, Default: []string{}}, {Name: countField, Type: manifest.Int, Default: 1, Min: &zero}}
	names := []string{originalValue}
	v, err := New(schema, map[string]any{namesField: names})
	require.NoError(t, err)
	names[0] = "changed"
	var read []string
	require.NoError(t, v.Get(namesField, &read))
	require.Equal(t, []string{originalValue}, read)
	read[0] = "changed"
	require.NoError(t, v.Get(namesField, &read))
	require.Equal(t, []string{originalValue}, read)
	require.ErrorContains(t, v.Get("other.secret", &read), "access denied")
	for _, values := range []map[string]any{{"extra": true}, {countField: -1}, {countField: 1.5}, {countField: "2"}, {countField: nil}, {namesField: []int{1}}} {
		_, err := New(schema, values)
		require.Error(t, err)
	}
	_, err = Decode(schema, strings.NewReader(`{"count":2} {}`))
	require.Error(t, err)
	v, err = Decode(schema, strings.NewReader(`{"count":9007199254740993}`))
	require.NoError(t, err)
	var large int64
	require.NoError(t, v.Get(countField, &large))
	require.Equal(t, int64(9007199254740993), large)
}
