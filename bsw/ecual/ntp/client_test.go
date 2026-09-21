package ntp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCancellationInterruptsUDPRead(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := (Client{}).Query(ctx, server.LocalAddr().String(), 3*time.Second); result <- err }()
	require.NoError(t, server.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = server.ReadFrom(make([]byte, 512))
	require.NoError(t, err)
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled NTP query did not stop its UDP read")
	}
}

func TestTimeoutBoundsUDPRead(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer server.Close()
	started := time.Now()
	_, err = (Client{}).Query(context.Background(), server.LocalAddr().String(), 30*time.Millisecond)
	require.Error(t, err)
	require.Less(t, time.Since(started), time.Second)
}
