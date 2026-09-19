package types

import "time"

type Aria2AddURIOptions struct {
	Dir         string
	Out         string
	Connections int
}

type Aria2DownloadStatus struct {
	GID             string      `json:"gid"`
	Status          string      `json:"status"`
	TotalLength     string      `json:"totalLength"`
	CompletedLength string      `json:"completedLength"`
	DownloadSpeed   string      `json:"downloadSpeed"`
	Dir             string      `json:"dir"`
	ErrorCode       string      `json:"errorCode"`
	ErrorMessage    string      `json:"errorMessage"`
	Files           []Aria2File `json:"files"`
	Bittorrent      *Aria2BT    `json:"bittorrent,omitempty"`
}

type Aria2BT struct {
	Info *Aria2BTInfo `json:"info,omitempty"`
}

type Aria2BTInfo struct {
	Name string `json:"name,omitempty"`
}

type Aria2File struct {
	Path            string     `json:"path"`
	Length          string     `json:"length"`
	CompletedLength string     `json:"completedLength"`
	URIs            []Aria2URI `json:"uris"`
}

type Aria2URI struct {
	URI string `json:"uri"`
}

type Aria2TaskRecord struct {
	PauseOwner   string        `json:"pause_owner,omitempty"`
	Deleted      bool          `json:"deleted,omitempty"`
	ControlUntil time.Time     `json:"control_until,omitzero"`
	State        DownloadState `json:"state"`
	Revision     uint64        `json:"revision"`
	GID          string        `json:"gid"`
	TaskID       string        `json:"task_id"`
	DownloadURL  string        `json:"download_url"`
	Dir          string        `json:"dir"`
	Out          string        `json:"out"`
	CreatedAt    time.Time     `json:"created_at"`
	Status       string        `json:"status"`
	Total        int64         `json:"total"`
	Completed    int64         `json:"completed"`
	Error        string        `json:"error,omitempty"`
}

type Aria2TaskInfo struct {
	GID             string
	Status          string
	TotalLength     int64
	CompletedLength int64
	RemainingLength int64
	ErrorCode       string
	ErrorMessage    string
}

type Aria2Overview struct {
	TotalTasks       int
	RemainingTasks   int
	RemainingBytes   int64
	StatusCounts     map[string]int
	RetryCandidates  []Aria2TaskInfo
	RetryBytes       int64
	RetryStatusCount map[string]int
}

type Aria2ActionResult struct {
	Matched int
	Changed int
	Skipped int
	Errors  []string
}

type Aria2Config struct {
	RPCURL         string `json:"rpc_url"`
	Secret         string `json:"secret"`
	Dir            string `json:"dir"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	AutoDownload   bool   `json:"auto_download"`
}
