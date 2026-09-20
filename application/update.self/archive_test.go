package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractRequiresCurrentExecutableName(t *testing.T) {
	for _, extension := range []string{zipExtension, tarGzExtension} {
		for _, name := range []string{executableFileName(), "old-" + executableFileName(), "nested/" + executableFileName()} {
			t.Run(extension+"/"+name, func(t *testing.T) {
				var buffer bytes.Buffer
				const payload = "test executable"
				if extension == zipExtension {
					writer := zip.NewWriter(&buffer)
					entry, err := writer.Create(name)
					require.NoError(t, err)
					_, err = entry.Write([]byte(payload))
					require.NoError(t, err)
					require.NoError(t, writer.Close())
				} else {
					compressed := gzip.NewWriter(&buffer)
					writer := tar.NewWriter(compressed)
					require.NoError(t, writer.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}))
					_, err := writer.Write([]byte(payload))
					require.NoError(t, err)
					require.NoError(t, writer.Close())
					require.NoError(t, compressed.Close())
				}
				dir := t.TempDir()
				archive := filepath.Join(dir, "release"+extension)
				require.NoError(t, os.WriteFile(archive, buffer.Bytes(), 0o600))
				path, err := extractExecutable(archive, filepath.Base(archive), dir)
				if name != executableFileName() {
					require.Error(t, err)
					require.NoFileExists(t, filepath.Join(dir, executableFileName()))
					return
				}
				require.NoError(t, err)
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, payload, string(data))
			})
		}
	}
}

func TestExtractRejectsRetiredArchiveFormats(t *testing.T) {
	for _, name := range []string{windowsExecutable, "tdl.tgz", unixExecutable} {
		_, err := extractExecutable("unused", name, t.TempDir())
		require.ErrorContains(t, err, "unsupported update archive")
	}
}
