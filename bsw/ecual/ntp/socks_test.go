package ntp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

func newSOCKSRelay(t *testing.T, authenticate, respond bool) (string, <-chan string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close(); _ = udp.Close() })
	target := make(chan string, 1)
	result := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		err = serveSOCKSRelay(conn, udp, authenticate, respond, target)
		result <- err
	}()
	url := "socks5://" + listener.Addr().String()
	if authenticate {
		url = "socks5://user:secret@" + listener.Addr().String()
	}
	return url, target, result
}

func serveSOCKSRelay(conn net.Conn, udp net.PacketConn, authenticate, respond bool, target chan<- string) error {
	greeting := make([]byte, 3)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return err
	}
	method := byte(0)
	if authenticate {
		method = 2
	}
	if !bytes.Equal(greeting, []byte{5, 1, method}) {
		return fmt.Errorf("incorrect SOCKS5 greeting")
	}
	if _, err := conn.Write([]byte{5, method}); err != nil {
		return err
	}
	if authenticate {
		auth := make([]byte, 2)
		if _, err := io.ReadFull(conn, auth); err != nil {
			return err
		}
		username := make([]byte, int(auth[1])+1)
		if _, err := io.ReadFull(conn, username); err != nil {
			return err
		}
		password := make([]byte, int(username[len(username)-1]))
		if _, err := io.ReadFull(conn, password); err != nil {
			return err
		}
		if auth[0] != 1 || string(username[:len(username)-1]) != "user" || string(password) != "secret" {
			return fmt.Errorf("incorrect proxy credentials")
		}
		if _, err := conn.Write([]byte{1, 0}); err != nil {
			return err
		}
	}
	command := make([]byte, 3)
	if _, err := io.ReadFull(conn, command); err != nil {
		return err
	}
	if !bytes.Equal(command, []byte{5, 3, 0}) {
		return fmt.Errorf("expected UDP ASSOCIATE")
	}
	if _, err := readSOCKSAddress(conn); err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(udp.LocalAddr().String())
	address, err := socksAddress(net.JoinHostPort("0.0.0.0", port))
	if err != nil {
		return err
	}
	if _, err := conn.Write(append([]byte{5, 0, 0}, address...)); err != nil {
		return err
	}
	_ = udp.SetReadDeadline(time.Now().Add(2 * time.Second))
	packet := make([]byte, 512)
	n, peer, err := udp.ReadFrom(packet)
	if err != nil {
		return err
	}
	if n < 4 || !bytes.Equal(packet[:3], []byte{0, 0, 0}) {
		return fmt.Errorf("invalid SOCKS5 UDP request")
	}
	reader := bytes.NewReader(packet[3:n])
	host, err := readSOCKSAddress(reader)
	if err != nil {
		return err
	}
	target <- host
	headerLength := n - reader.Len()
	if reader.Len() < 48 {
		return fmt.Errorf("short proxied NTP request")
	}
	if respond {
		reply := append(append([]byte(nil), packet[:headerLength]...), ntpReply(packet[headerLength:n])...)
		if _, err := udp.WriteTo(reply, peer); err != nil {
			return err
		}
	}
	// A completed or cancelled query must close the TCP control connection.
	_, err = conn.Read(make([]byte, 1))
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("proxy control was not closed: %w", err)
	}
	return nil
}

func TestSOCKS5NTPAuthenticatesAndResolvesNamesAtProxy(t *testing.T) {
	for _, authenticate := range []bool{false, true} {
		t.Run(fmt.Sprint(authenticate), func(t *testing.T) {
			url, target, result := newSOCKSRelay(t, authenticate, true)
			sample, err := (Client{}).QueryViaProxy(context.Background(), "ntp.test.invalid", url, time.Second)
			require.NoError(t, err)
			require.Equal(t, "ntp.test.invalid:123", <-target, "client must not require local DNS for the NTP host")
			require.Equal(t, types.ClockSourceNTP, sample.Source)
			require.InDelta(t, float64(time.Hour), float64(sample.Offset), float64(100*time.Millisecond))
			require.NoError(t, <-result)
		})
	}
}

func TestSOCKS5CancellationAndTimeoutCloseBothSockets(t *testing.T) {
	for _, cancelQuery := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelQuery), func(t *testing.T) {
			url, target, relayResult := newSOCKSRelay(t, false, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			timeout := 100 * time.Millisecond
			if cancelQuery {
				timeout = 3 * time.Second
			}
			go func() { _, err := (Client{}).QueryViaProxy(ctx, "ntp.test.invalid", url, timeout); result <- err }()
			select {
			case <-target:
			case <-time.After(time.Second):
				t.Fatal("proxy did not receive the UDP query")
			}
			if cancelQuery {
				cancel()
			}
			select {
			case err := <-result:
				if cancelQuery {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.Error(t, err)
				}
			case <-time.After(time.Second):
				t.Fatal("proxy query did not stop")
			}
			require.NoError(t, <-relayResult)
		})
	}
}

func TestSOCKSAddressesAndMalformedHeaders(t *testing.T) {
	for _, address := range []string{"127.0.0.1:123", "[::1]:123", "test:123", "ntp.test.invalid:123"} {
		encoded, err := socksAddress(address)
		require.NoError(t, err)
		decoded, err := readSOCKSAddress(bytes.NewReader(encoded))
		require.NoError(t, err)
		require.Equal(t, address, decoded)
	}
	for _, packet := range [][]byte{{}, {9}, {3, 0}, {1, 127}, {3, 4, 't'}} {
		_, err := readSOCKSAddress(bytes.NewReader(packet))
		require.Error(t, err)
	}
}
