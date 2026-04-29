package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-faster/errors"

	"github.com/iyear/tdl/pkg/config"
)

const aria2BoolTrue = "true"

type aria2AddURIOptions struct {
	Dir string
	Out string
}

type aria2Client struct {
	rpcURL     string
	secret     string
	httpClient *http.Client
}

func newAria2Client(cfg config.Aria2Config) *aria2Client {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &aria2Client{
		rpcURL: cfg.RPCURL,
		secret: cfg.Secret,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

type aria2RPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	ID      string `json:"id"`
	Params  []any  `json:"params"`
}

type aria2RPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *aria2RPCError  `json:"error"`
	ID     string          `json:"id"`
	Extra  json.RawMessage `json:"-"`
}

type aria2RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func isAria2ConnectionError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	return errors.Is(err, context.DeadlineExceeded)
}

func (c *aria2Client) callRaw(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	if c.rpcURL == "" {
		return nil, errors.New("aria2 rpc_url is empty")
	}

	if c.secret != "" {
		params = append([]any{"token:" + c.secret}, params...)
	}

	body, err := json.Marshal(aria2RPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		ID:      "tdl-watch",
		Params:  params,
	})
	if err != nil {
		return nil, errors.Wrap(err, "marshal aria2 request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrap(err, "create aria2 request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "do aria2 request")
	}
	defer resp.Body.Close()

	var decoded aria2RPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, errors.Wrap(err, "decode aria2 response")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decoded.Error != nil {
			return nil, fmt.Errorf("aria2 rpc status %d: %s", resp.StatusCode, decoded.Error.Message)
		}
		return nil, fmt.Errorf("aria2 rpc status %d", resp.StatusCode)
	}

	if decoded.Error != nil {
		return nil, fmt.Errorf("aria2 rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}

	return decoded.Result, nil
}

func (c *aria2Client) callString(ctx context.Context, method string, params []any) (string, error) {
	raw, err := c.callRaw(ctx, method, params)
	if err != nil {
		return "", err
	}

	var result string
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", errors.Wrap(err, "decode aria2 string result")
	}

	return result, nil
}

func (c *aria2Client) AddURI(ctx context.Context, uri string, opts aria2AddURIOptions) (string, error) {
	params := make([]any, 0, 2)
	params = append(params, []string{uri})

	options := map[string]any{}
	if opts.Dir != "" {
		options["dir"] = opts.Dir
	}
	if opts.Out != "" {
		options["out"] = opts.Out
	}
	// tdl streams Telegram chunks concurrently itself. Letting aria2 split the
	// same tdl URL into multiple HTTP ranges adds failure surface without adding
	// Telegram-side concurrency.
	options["split"] = "1"
	options["max-connection-per-server"] = "1"
	options["continue"] = aria2BoolTrue
	options["allow-piece-length-change"] = aria2BoolTrue
	options["allow-overwrite"] = aria2BoolTrue
	options["auto-file-renaming"] = "false"
	options["user-agent"] = "tdl-watch-aria2"
	if len(options) > 0 {
		params = append(params, options)
	}

	result, err := c.callString(ctx, "aria2.addUri", params)
	if err != nil {
		return "", err
	}
	if result == "" {
		return "", errors.New("aria2 rpc returned empty gid")
	}

	return result, nil
}

func (c *aria2Client) SetMaxConcurrentDownloads(ctx context.Context, limit int) error {
	if limit < 1 {
		return errors.New("aria2 max concurrent downloads must be greater than 0")
	}

	result, err := c.callString(ctx, "aria2.changeGlobalOption", []any{
		map[string]any{
			"max-concurrent-downloads": strconv.Itoa(limit),
		},
	})
	if err != nil {
		return err
	}
	if result != "OK" {
		return fmt.Errorf("unexpected aria2 response %q", result)
	}
	return nil
}

func (c *aria2Client) GetGlobalDir(ctx context.Context) (string, error) {
	raw, err := c.callRaw(ctx, "aria2.getGlobalOption", []any{})
	if err != nil {
		return "", err
	}

	var options map[string]string
	if err := json.Unmarshal(raw, &options); err != nil {
		return "", errors.Wrap(err, "decode aria2 global options")
	}
	return options["dir"], nil
}

type aria2DownloadStatus struct {
	GID             string      `json:"gid"`
	Status          string      `json:"status"`
	TotalLength     string      `json:"totalLength"`
	CompletedLength string      `json:"completedLength"`
	ErrorCode       string      `json:"errorCode"`
	ErrorMessage    string      `json:"errorMessage"`
	Files           []aria2File `json:"files"`
}

type aria2File struct {
	Path            string     `json:"path"`
	Length          string     `json:"length"`
	CompletedLength string     `json:"completedLength"`
	URIs            []aria2URI `json:"uris"`
}

type aria2URI struct {
	URI string `json:"uri"`
}

var aria2StatusKeys = []string{"gid", "status", "totalLength", "completedLength", "errorCode", "errorMessage", "files"}

func (c *aria2Client) TellActive(ctx context.Context) ([]aria2DownloadStatus, error) {
	raw, err := c.callRaw(ctx, "aria2.tellActive", []any{aria2StatusKeys})
	if err != nil {
		return nil, err
	}

	var result []aria2DownloadStatus
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.Wrap(err, "decode aria2 active tasks")
	}
	return result, nil
}

func (c *aria2Client) TellWaiting(ctx context.Context, offset, num int) ([]aria2DownloadStatus, error) {
	raw, err := c.callRaw(ctx, "aria2.tellWaiting", []any{offset, num, aria2StatusKeys})
	if err != nil {
		return nil, err
	}

	var result []aria2DownloadStatus
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.Wrap(err, "decode aria2 waiting tasks")
	}
	return result, nil
}

func (c *aria2Client) TellStopped(ctx context.Context, offset, num int) ([]aria2DownloadStatus, error) {
	raw, err := c.callRaw(ctx, "aria2.tellStopped", []any{offset, num, aria2StatusKeys})
	if err != nil {
		return nil, err
	}

	var result []aria2DownloadStatus
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.Wrap(err, "decode aria2 stopped tasks")
	}
	return result, nil
}

func (c *aria2Client) ForcePause(ctx context.Context, gid string) error {
	result, err := c.callString(ctx, "aria2.forcePause", []any{gid})
	if err != nil {
		return err
	}
	if result != gid {
		return fmt.Errorf("unexpected aria2 forcePause response %q", result)
	}
	return nil
}

func (c *aria2Client) Unpause(ctx context.Context, gid string) error {
	result, err := c.callString(ctx, "aria2.unpause", []any{gid})
	if err != nil {
		return err
	}
	if result != gid {
		return fmt.Errorf("unexpected aria2 unpause response %q", result)
	}
	return nil
}

func (c *aria2Client) RemoveDownloadResult(ctx context.Context, gid string) error {
	result, err := c.callString(ctx, "aria2.removeDownloadResult", []any{gid})
	if err != nil {
		return err
	}
	if result != "OK" {
		return fmt.Errorf("unexpected aria2 removeDownloadResult response %q", result)
	}
	return nil
}
