package aria2

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/bsw/ecual/aria2rpc"
	"github.com/snakexgc/tdl/pkg/config"
)

const (
	aria2BoolTrue          = "true"
	aria2BoolFalse         = "false"
	tdlAria2PieceSize      = "1024K"
	tdlAria2TimeoutSeconds = "600"
	tdlAria2UserAgent      = "tdl-watch-aria2"
	aria2KeyDir            = "dir"
)

type AddURIOptions struct {
	Dir         string
	Out         string
	Connections int
}

type aria2AddURIOptions = AddURIOptions

type Client struct {
	rpcURL     string
	secret     string
	httpClient *http.Client
}

func NewClient(cfg config.Aria2Config) *Client {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &Client{
		rpcURL: cfg.RPCURL,
		secret: cfg.Secret,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func IsConnectionError(err error) bool {
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

func (c *Client) callRaw(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	return aria2rpc.Call(ctx, c.httpClient, c.rpcURL, c.secret, method, params, 6)
}

func (c *Client) callString(ctx context.Context, method string, params []any) (string, error) {
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

func (c *Client) AddURI(ctx context.Context, uri string, opts AddURIOptions) (string, error) {
	params := make([]any, 0, 2)
	params = append(params, []string{uri})

	options := map[string]any{}
	if opts.Dir != "" {
		options[aria2KeyDir] = opts.Dir
	}
	if opts.Out != "" {
		options["out"] = opts.Out
	}
	applyTDLHTTPConnectionOptions(options, opts.Connections)
	options["continue"] = aria2BoolTrue
	options["allow-piece-length-change"] = aria2BoolTrue
	options["allow-overwrite"] = aria2BoolTrue
	options["auto-file-renaming"] = aria2BoolFalse
	options["user-agent"] = tdlAria2UserAgent
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

func applyTDLHTTPConnectionOptions(options map[string]any, connections int) {
	if connections < 1 {
		connections = 1
	}
	value := strconv.Itoa(connections)
	options["split"] = value
	options["max-connection-per-server"] = value
	options["min-split-size"] = tdlAria2PieceSize
	options["piece-length"] = tdlAria2PieceSize
	options["timeout"] = tdlAria2TimeoutSeconds
}

func (c *Client) AddTorrent(ctx context.Context, data []byte, opts AddURIOptions) (string, error) {
	if len(data) == 0 {
		return "", errors.New("torrent data is empty")
	}

	params := make([]any, 0, 2)
	params = append(params, base64.StdEncoding.EncodeToString(data))
	options := map[string]any{}
	if opts.Dir != "" {
		options[aria2KeyDir] = opts.Dir
	}
	if opts.Out != "" {
		options["out"] = opts.Out
	}
	if len(options) > 0 {
		params = append(params, []string{})
		params = append(params, options)
	}

	result, err := c.callString(ctx, "aria2.addTorrent", params)
	if err != nil {
		return "", err
	}
	if result == "" {
		return "", errors.New("aria2 rpc returned empty gid")
	}
	return result, nil
}

func (c *Client) SetMaxConcurrentDownloads(ctx context.Context, limit int) error {
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

func (c *Client) GetGlobalOptions(ctx context.Context) (map[string]string, error) {
	raw, err := c.callRaw(ctx, "aria2.getGlobalOption", []any{})
	if err != nil {
		return nil, err
	}

	var options map[string]string
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, errors.Wrap(err, "decode aria2 global options")
	}
	return options, nil
}

func (c *Client) GetGlobalDir(ctx context.Context) (string, error) {
	options, err := c.GetGlobalOptions(ctx)
	if err != nil {
		return "", err
	}
	return options["dir"], nil
}

type DownloadStatus struct {
	GID             string `json:"gid"`
	Status          string `json:"status"`
	TotalLength     string `json:"totalLength"`
	CompletedLength string `json:"completedLength"`
	DownloadSpeed   string `json:"downloadSpeed"`
	Dir             string `json:"dir"`
	ErrorCode       string `json:"errorCode"`
	ErrorMessage    string `json:"errorMessage"`
	Files           []File `json:"files"`
	Bittorrent      *BT    `json:"bittorrent,omitempty"`
}

type aria2DownloadStatus = DownloadStatus

type BT struct {
	Info *BTInfo `json:"info,omitempty"`
}

type BTInfo struct {
	Name string `json:"name,omitempty"`
}

type File struct {
	Path            string `json:"path"`
	Length          string `json:"length"`
	CompletedLength string `json:"completedLength"`
	URIs            []URI  `json:"uris"`
}

type aria2File = File

type URI struct {
	URI string `json:"uri"`
}

type aria2URI = URI

var aria2StatusKeys = []string{"gid", "status", "totalLength", "completedLength", "downloadSpeed", aria2KeyDir, "errorCode", "errorMessage", "files", "bittorrent"}

func (c *Client) TellStatus(ctx context.Context, gid string) (DownloadStatus, error) {
	raw, err := c.callRaw(ctx, "aria2.tellStatus", []any{gid, aria2StatusKeys})
	if err != nil {
		return DownloadStatus{}, err
	}

	var result DownloadStatus
	if err := json.Unmarshal(raw, &result); err != nil {
		return DownloadStatus{}, errors.Wrap(err, "decode aria2 task status")
	}
	return result, nil
}

func (c *Client) TellActive(ctx context.Context) ([]DownloadStatus, error) {
	raw, err := c.callRaw(ctx, "aria2.tellActive", []any{aria2StatusKeys})
	if err != nil {
		return nil, err
	}

	var result []DownloadStatus
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.Wrap(err, "decode aria2 active tasks")
	}
	return result, nil
}

func (c *Client) TellWaiting(ctx context.Context, offset, num int) ([]DownloadStatus, error) {
	raw, err := c.callRaw(ctx, "aria2.tellWaiting", []any{offset, num, aria2StatusKeys})
	if err != nil {
		return nil, err
	}

	var result []DownloadStatus
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.Wrap(err, "decode aria2 waiting tasks")
	}
	return result, nil
}

func (c *Client) TellStopped(ctx context.Context, offset, num int) ([]DownloadStatus, error) {
	raw, err := c.callRaw(ctx, "aria2.tellStopped", []any{offset, num, aria2StatusKeys})
	if err != nil {
		return nil, err
	}

	var result []DownloadStatus
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.Wrap(err, "decode aria2 stopped tasks")
	}
	return result, nil
}

func (c *Client) ForcePause(ctx context.Context, gid string) error {
	result, err := c.callString(ctx, "aria2.forcePause", []any{gid})
	if err != nil {
		return err
	}
	if result != gid {
		return fmt.Errorf("unexpected aria2 forcePause response %q", result)
	}
	return nil
}

func (c *Client) Pause(ctx context.Context, gid string) error {
	result, err := c.callString(ctx, "aria2.pause", []any{gid})
	if err != nil {
		return err
	}
	if result != gid {
		return fmt.Errorf("unexpected aria2 pause response %q", result)
	}
	return nil
}

func (c *Client) Unpause(ctx context.Context, gid string) error {
	result, err := c.callString(ctx, "aria2.unpause", []any{gid})
	if err != nil {
		return err
	}
	if result != gid {
		return fmt.Errorf("unexpected aria2 unpause response %q", result)
	}
	return nil
}

func (c *Client) Remove(ctx context.Context, gid string) error {
	result, err := c.callString(ctx, "aria2.remove", []any{gid})
	if err != nil {
		return err
	}
	if result != gid {
		return fmt.Errorf("unexpected aria2 remove response %q", result)
	}
	return nil
}

func (c *Client) RemoveDownloadResult(ctx context.Context, gid string) error {
	result, err := c.callString(ctx, "aria2.removeDownloadResult", []any{gid})
	if err != nil {
		return err
	}
	if result != "OK" {
		return fmt.Errorf("unexpected aria2 removeDownloadResult response %q", result)
	}
	return nil
}
