package ntp

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

const validResponse = "valid"

func ntpTimestamp(at time.Time) uint64 {
	const epoch = 2208988800
	return uint64(uint32(at.Unix()+epoch))<<32 | (uint64(at.Nanosecond())<<32)/uint64(time.Second)
}

func ntpReply(request []byte) []byte {
	at := time.Now().Add(time.Hour)
	packet := make([]byte, 48)
	packet[0], packet[1] = 0x24, 2 // v4 server, synchronized stratum 2.
	copy(packet[24:32], request[40:48])
	binary.BigEndian.PutUint64(packet[16:24], ntpTimestamp(at.Add(-time.Second)))
	binary.BigEndian.PutUint64(packet[32:40], ntpTimestamp(at))
	binary.BigEndian.PutUint64(packet[40:48], ntpTimestamp(at))
	return packet
}

func TestNTPValidatesWireResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]byte) []byte
		valid  bool
	}{
		{name: validResponse, valid: true},
		{name: "unsynchronized", mutate: func(p []byte) []byte { p[0] |= 0xc0; return p }},
		{name: "kiss_of_death", mutate: func(p []byte) []byte { p[1] = 0; copy(p[12:16], "RATE"); return p }},
		{name: "invalid_stratum", mutate: func(p []byte) []byte { p[1] = 16; return p }},
		{name: "wrong_origin", mutate: func(p []byte) []byte { p[24] ^= 1; return p }},
		{name: "wrong_mode", mutate: func(p []byte) []byte { p[0] = 0x23; return p }},
		{name: "truncated", mutate: func(p []byte) []byte { return p[:20] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, err := net.ListenPacket("udp", "127.0.0.1:0")
			require.NoError(t, err)
			defer server.Close()
			result := make(chan error, 1)
			go func() {
				_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
				request := make([]byte, 512)
				n, peer, err := server.ReadFrom(request)
				if err == nil && n < 48 {
					err = fmt.Errorf("short NTP request")
				}
				if err == nil {
					packet := ntpReply(request[:n])
					if test.mutate != nil {
						packet = test.mutate(packet)
					}
					_, err = server.WriteTo(packet, peer)
				}
				result <- err
			}()
			sample, err := (Client{}).Query(context.Background(), server.LocalAddr().String(), time.Second)
			if test.valid {
				require.NoError(t, err)
				require.InDelta(t, float64(time.Hour), float64(sample.Offset), float64(100*time.Millisecond))
				require.Equal(t, types.ClockSourceNTP, sample.Source)
				require.False(t, sample.MeasuredAt.IsZero())
			} else {
				require.Error(t, err)
			}
			require.NoError(t, <-result)
		})
	}
}
