package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-faster/errors"

	"github.com/iyear/tdl/pkg/config"
)

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
	Result string          `json:"result"`
	Error  *aria2RPCError  `json:"error"`
	ID     string          `json:"id"`
	Extra  json.RawMessage `json:"-"`
}

type aria2RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *aria2Client) AddURI(ctx context.Context, uri string, opts aria2AddURIOptions) (string, error) {
	if c.rpcURL == "" {
		return "", errors.New("aria2 rpc_url is empty")
	}

	params := make([]any, 0, 3)
	if c.secret != "" {
		params = append(params, "token:"+c.secret)
	}
	params = append(params, []string{uri})

	options := map[string]any{}
	if opts.Dir != "" {
		options["dir"] = opts.Dir
	}
	if opts.Out != "" {
		options["out"] = opts.Out
	}
	// tdl is already proxying Telegram chunks; keep aria2 from splitting the
	// proxy URL into competing range requests that may cancel each other.
	options["split"] = "1"
	options["max-connection-per-server"] = "1"
	options["allow-piece-length-change"] = "true"
	options["allow-overwrite"] = "true"
	options["auto-file-renaming"] = "false"
	options["user-agent"] = "tdl-watch-aria2"
	options["header"] = []string{"Range: bytes=0-"}
	if len(options) > 0 {
		params = append(params, options)
	}

	body, err := json.Marshal(aria2RPCRequest{
		JSONRPC: "2.0",
		Method:  "aria2.addUri",
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
	if decoded.Result == "" {
		return "", errors.New("aria2 rpc returned empty gid")
	}

	return decoded.Result, nil
}
