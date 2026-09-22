// Package aria2rpc is the shared JSON-RPC transport for control, executors and AriaNg.
package aria2rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-faster/errors"
)

type Request struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	ID      string `json:"id"`
	Params  []any  `json:"params"`
}

type Response struct {
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
	ID     string          `json:"id"`
	Extra  json.RawMessage `json:"-"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func Call(ctx context.Context, client *http.Client, rpcURL, secret, method string, params []any, attempts int) (json.RawMessage, error) {
	if rpcURL == "" {
		return nil, errors.New("aria2 rpc_url is empty")
	}

	if secret != "" {
		params = append([]any{"token:" + secret}, params...)
	}

	body, err := json.Marshal(Request{
		JSONRPC: "2.0",
		Method:  method,
		ID:      "tdl-watch",
		Params:  params,
	})
	if err != nil {
		return nil, errors.Wrap(err, "marshal aria2 request")
	}

	const retryDelay = time.Second
	if attempts < 1 {
		attempts = 1
	}

	var resp *http.Response
	for attempt := range attempts {
		resp, err = Forward(ctx, client, rpcURL, body)
		if err == nil {
			break
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || attempt == attempts-1 {
			return nil, errors.Wrap(err, "do aria2 request")
		}
		select {
		case <-ctx.Done():
			return nil, errors.Wrap(ctx.Err(), "do aria2 request")
		case <-time.After(retryDelay):
		}
	}
	defer resp.Body.Close()

	var decoded Response
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

// Forward performs exactly one request, preserving batch envelopes and request
// IDs. Authorization rewriting is the caller's policy, not a transport concern.
func Forward(ctx context.Context, client *http.Client, rpcURL string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}
