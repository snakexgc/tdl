package ntp

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

// Ordinary HTTP CONNECT cannot carry UDP NTP. These HTTPS origins provide a
// lower precision Date header fallback through the same explicitly set proxy.
// Never infer an arbitrary HTTPS endpoint from a user-supplied NTP address.
var httpsTimeSources = map[string]string{
	"ntp.aliyun.com":          "https://www.aliyun.com/",
	"time1.cloud.tencent.com": "https://cloud.tencent.com/",
	"time.apple.com":          "https://www.apple.com/",
	"time.google.com":         "https://www.google.com/",
	"time.windows.com":        "https://www.microsoft.com/",
	"time.cloudflare.com":     "https://www.cloudflare.com/cdn-cgi/trace",
}

func queryHTTPS(ctx context.Context, host, proxyURL string) (types.TimeSample, error) {
	endpoint := httpsTimeSources[strings.ToLower(host)]
	if endpoint == "" {
		return types.TimeSample{}, fmt.Errorf("no HTTPS time fallback for this NTP server")
	}
	client, transport, err := newHTTPSClient(ctx, proxyURL)
	if err != nil {
		return types.TimeSample{}, err
	}
	defer transport.CloseIdleConnections()
	return queryHTTPSWithClient(ctx, endpoint, client)
}

func newHTTPSClient(ctx context.Context, proxyURL string) (*http.Client, *http.Transport, error) {
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != proxySchemeHTTP && parsed.Scheme != proxySchemeHTTPS && parsed.Scheme != proxySchemeSOCKS5) {
		return nil, nil, fmt.Errorf("time proxy is unavailable")
	}
	// Transport may detach its connection setup from the request's cancellation
	// for connection reuse. This client owns one query: tie the underlying socket
	// to its original context so stalled CONNECT and TLS handshakes also stop.
	transport := &http.Transport{Proxy: http.ProxyURL(parsed), DisableKeepAlives: true, DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		if deadline, ok := ctx.Deadline(); ok {
			if err := conn.SetDeadline(deadline); err != nil {
				_ = conn.Close()
				return nil, err
			}
		}
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		return &timeQueryConn{Conn: conn, stop: stop}, nil
	}}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, transport, nil
}

type timeQueryConn struct {
	net.Conn
	stop func() bool
}

func (c *timeQueryConn) Close() error {
	c.stop()
	return c.Conn.Close()
}

func queryHTTPSWithClient(ctx context.Context, endpoint string, client *http.Client) (types.TimeSample, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != proxySchemeHTTPS || parsed.Host == "" {
		return types.TimeSample{}, fmt.Errorf("HTTPS time source is required")
	}
	// A random cache key avoids old CDN Date headers even when a proxy ignores
	// request cache directives. Age-bearing cached responses are also rejected.
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return types.TimeSample{}, err
	}
	query := parsed.Query()
	query.Set("tdl_time", fmt.Sprintf("%x", nonce))
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, parsed.String(), nil)
	if err != nil {
		return types.TimeSample{}, err
	}
	request.Header.Set("Cache-Control", "no-cache, no-store, max-age=0")
	request.Header.Set("Pragma", "no-cache")
	started := time.Now()
	response, err := client.Do(request)
	measuredAt := started.Add(time.Since(started))
	if err != nil {
		if ctx.Err() != nil {
			return types.TimeSample{}, ctx.Err()
		}
		return types.TimeSample{}, fmt.Errorf("HTTPS time request failed")
	}
	defer response.Body.Close()
	if err := ctx.Err(); err != nil {
		return types.TimeSample{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return types.TimeSample{}, fmt.Errorf("HTTPS time source returned status %d", response.StatusCode)
	}
	if age := response.Header.Get("Age"); age != "" && age != "0" {
		return types.TimeSample{}, fmt.Errorf("cached HTTPS time response")
	}
	date, err := http.ParseTime(response.Header.Get("Date"))
	if err != nil {
		return types.TimeSample{}, fmt.Errorf("invalid HTTPS time response")
	}
	// Date has one-second resolution. Use the middle of that second and half
	// the round trip as an estimate of current origin time at receipt.
	elapsed := measuredAt.Sub(started)
	corrected := date.Add(time.Second/2 + elapsed/2)
	return types.TimeSample{
		Offset: corrected.Sub(measuredAt), Elapsed: elapsed,
		MeasuredAt: measuredAt, Source: types.ClockSourceHTTPS, Server: endpoint,
	}, nil
}
