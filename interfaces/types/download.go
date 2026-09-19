package types

// DownloadSubmission carries metadata only. File bytes stay on the data path.
type DownloadSubmission struct {
	Account     AccountID
	TaskID      string
	DownloadURL string
	Dir         string
	Out         string
	FullPath    string
}

type DownloadResult struct {
	Account AccountID
	Target  string
	ID      string
	Skipped bool
}
