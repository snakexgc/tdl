package bot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoginNamespaceFromCommand(t *testing.T) {
	namespace, err := loginNamespaceFromCommand("/login_code Alice")
	require.NoError(t, err)
	require.Equal(t, "Alice", namespace)

	_, err = loginNamespaceFromCommand("/login_code")
	require.Error(t, err)

	_, err = loginNamespaceFromCommand("/login_code alice1")
	require.Error(t, err)
}
