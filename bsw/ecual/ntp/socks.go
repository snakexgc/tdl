package ntp

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"sync"
)

// SOCKS5 UDP ASSOCIATE (RFC 1928). The TCP control connection owns the UDP
// association and must remain open until the NTP query is closed or cancelled.
func dialSOCKSUDP(ctx context.Context, proxyURL *url.URL, remote string) (net.Conn, error) {
	port := proxyURL.Port()
	if port == "" {
		port = "1080"
	}
	control, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(proxyURL.Hostname(), port))
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = control.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = control.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := control.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	if err := socksAuthenticate(control, proxyURL.User); err != nil {
		return nil, err
	}
	// An unspecified client address lets the relay infer the outbound endpoint,
	// including clients behind NAT or inside a container.
	if _, err := control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return nil, err
	}
	header := make([]byte, 3)
	if _, err := io.ReadFull(control, header); err != nil {
		return nil, err
	}
	if header[0] != 5 || header[1] != 0 || header[2] != 0 {
		return nil, fmt.Errorf("SOCKS5 UDP association rejected")
	}
	relay, err := readSOCKSAddress(control)
	if err != nil {
		return nil, err
	}
	relayHost, relayPort, err := net.SplitHostPort(relay)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(relayHost); ip != nil && ip.IsUnspecified() {
		// Many local proxies return 0.0.0.0 or :: for the UDP relay.
		relayHost, _, err = net.SplitHostPort(control.RemoteAddr().String())
		if err != nil {
			return nil, err
		}
	}
	udp, err := (&net.Dialer{}).DialContext(ctx, "udp", net.JoinHostPort(relayHost, relayPort))
	if err != nil {
		return nil, err
	}
	target, err := socksAddress(remote)
	if err != nil {
		_ = udp.Close()
		return nil, err
	}
	conn := &socksUDPConn{Conn: udp, control: control, prefix: append([]byte{0, 0, 0}, target...)}
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	success = true
	return conn, nil
}

func socksAuthenticate(conn net.Conn, credentials *url.Userinfo) error {
	method := byte(0)
	if credentials != nil {
		method = 2
	}
	if _, err := conn.Write([]byte{5, 1, method}); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[0] != 5 || reply[1] != method {
		return fmt.Errorf("SOCKS5 authentication method rejected")
	}
	if credentials == nil {
		return nil
	}
	username := credentials.Username()
	password, _ := credentials.Password()
	if len(username) == 0 || len(username) > 255 || len(password) > 255 {
		return fmt.Errorf("invalid SOCKS5 credential length")
	}
	request := append([]byte{1, byte(len(username))}, []byte(username)...)
	request = append(request, byte(len(password)))
	request = append(request, []byte(password)...)
	if _, err := conn.Write(request); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[0] != 1 || reply[1] != 0 {
		return fmt.Errorf("SOCKS5 authentication failed")
	}
	return nil
}

func socksAddress(address string) ([]byte, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return nil, fmt.Errorf("invalid SOCKS5 destination port")
	}
	var result []byte
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			result = append([]byte{1}, v4...)
		} else {
			result = append([]byte{4}, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("invalid SOCKS5 destination hostname")
		}
		// Preserve the hostname so DNS is resolved by the proxy.
		result = append([]byte{3, byte(len(host))}, []byte(host)...)
	}
	return binary.BigEndian.AppendUint16(result, uint16(number)), nil
}

func readSOCKSAddress(reader io.Reader) (string, error) {
	kind := make([]byte, 1)
	if _, err := io.ReadFull(reader, kind); err != nil {
		return "", err
	}
	addressType := kind[0]
	var length int
	switch addressType {
	case 1:
		length = net.IPv4len
	case 4:
		length = net.IPv6len
	case 3:
		if _, err := io.ReadFull(reader, kind); err != nil {
			return "", err
		}
		length = int(kind[0])
		if length == 0 {
			return "", fmt.Errorf("empty SOCKS5 hostname")
		}
	default:
		return "", fmt.Errorf("invalid SOCKS5 address type")
	}
	address := make([]byte, length+2)
	if _, err := io.ReadFull(reader, address); err != nil {
		return "", err
	}
	host := string(address[:length])
	if addressType != 3 {
		host = net.IP(address[:length]).String()
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(address[length:])))), nil
}

type socksUDPConn struct {
	net.Conn
	control net.Conn
	prefix  []byte
	once    sync.Once
}

func (c *socksUDPConn) Write(packet []byte) (int, error) {
	message := append(append([]byte(nil), c.prefix...), packet...)
	n, err := c.Conn.Write(message)
	if n != len(message) && err == nil {
		err = io.ErrShortWrite
	}
	if err != nil {
		return 0, err
	}
	return len(packet), nil
}

func (c *socksUDPConn) Read(packet []byte) (int, error) {
	message := make([]byte, len(packet)+262)
	n, err := c.Conn.Read(message)
	if err != nil {
		return 0, err
	}
	if n < 4 || message[0] != 0 || message[1] != 0 || message[2] != 0 {
		return 0, fmt.Errorf("invalid or fragmented SOCKS5 UDP response")
	}
	reader := bytes.NewReader(message[3:n])
	if _, err := readSOCKSAddress(reader); err != nil {
		return 0, err
	}
	return reader.Read(packet)
}

func (c *socksUDPConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.Conn.Close()
		_ = c.control.Close()
	})
	return err
}
