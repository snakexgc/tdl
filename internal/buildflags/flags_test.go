package buildflags

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestDockerAndReleaseUseIdenticalMetadata(t *testing.T) {
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	require.NoError(t, err)
	var release struct {
		Builds []struct {
			LDFlags []string `yaml:"ldflags"`
		} `yaml:"builds"`
	}
	require.NoError(t, yaml.Unmarshal(data, &release))
	require.Len(t, release.Builds, 1)
	require.Len(t, release.Builds[0].LDFlags, 1)
	tpl, err := template.New("release").Funcs(template.FuncMap{"mustReadFile": func(path string) (string, error) {
		data, err := os.ReadFile(filepath.Join(root, path))
		return string(data), err
	}}).Parse(release.Builds[0].LDFlags[0])
	require.NoError(t, err)
	for _, arm := range []string{"", "5", "6", "7"} {
		const version, commit, date = "v202609181", "abc1234", "2026-09-18T10:30:00Z"
		docker, err := Render(version, commit, date, arm)
		require.NoError(t, err)
		var output bytes.Buffer
		require.NoError(t, tpl.Execute(&output, map[string]any{
			"Env": map[string]string{"RELEASE_VERSION": version}, "Version": "dev",
			"ShortCommit": commit, "CommitDate": date, "Arm": arm,
		}))
		require.Equal(t, docker, strings.TrimSpace(output.String()))
		require.Contains(t, docker, "consts.Version="+version)
		require.Contains(t, docker, "consts.GOARM="+arm)
	}
}

func TestRejectsMetadataThatChangesLinkerArguments(t *testing.T) {
	for _, value := range []string{"dev -X other=value", "dev\n", "'dev'", "\"dev\"", "dev\x00"} {
		_, err := Render(value, "commit", "date", "")
		require.Error(t, err)
	}
}
