package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-faster/errors"

	"github.com/iyear/tdl/pkg/config"
)

const aria2BoolTrue = "true"

type aria2AddURIOptions struct {
	Dir         string
	Out         string
	Connections int
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
	Result string          `json:"result"`
	Error  *aria2RPCError  `json:"error"`
	ID     string          `json:"id"`
	Extra  json.RawMessage `json:"-"`
}

type aria2RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *aria2Client) call(ctx context.Context, method string, params []any) (string, error) {
	if c.rpcURL == "" {
		return "", errors.New("aria2 rpc_url is empty")
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
		return "", errors.Wrap(err, "marshal aria2 request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(body))
	if err != nil {
		return "", errors.Wrap(err, "create aria2 request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "do aria2 request")
	}
	defer resp.Body.Close()

	var decoded aria2RPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", errors.Wrap(err, "decode aria2 response")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decoded.Error != nil {
			return "", fmt.Errorf("aria2 rpc status %d: %s", resp.StatusCode, decoded.Error.Message)
		}
		return "", fmt.Errorf("aria2 rpc status %d", resp.StatusCode)
	}

	if decoded.Error != nil {
		return "", fmt.Errorf("aria2 rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}

	return decoded.Result, nil
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
	connections := normalizeAria2Connections(opts.Connections)
	options["split"] = strconv.Itoa(connections)
	options["max-connection-per-server"] = strconv.Itoa(connections)
	options["continue"] = aria2BoolTrue
	if connections > 1 {
		options["min-split-size"] = "1M"
	}
	options["allow-piece-length-change"] = aria2BoolTrue
	options["allow-overwrite"] = aria2BoolTrue
	options["auto-file-renaming"] = "false"
	options["user-agent"] = "tdl-watch-aria2"
	if len(options) > 0 {
		params = append(params, options)
	}

	result, err := c.call(ctx, "aria2.addUri", params)
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

	result, err := c.call(ctx, "aria2.changeGlobalOption", []any{
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

func normalizeAria2Connections(connections int) int {
	if connections < 1 {
		return 1
	}
	return connections
}
