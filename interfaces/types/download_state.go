package types

import "strings"

// NormalizeDownloadState is shared by persistence and public task queries.
func NormalizeDownloadState(status string) DownloadState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "waiting", string(DownloadQueued):
		return DownloadQueued
	case string(DownloadActive):
		return DownloadActive
	case string(DownloadPaused):
		return DownloadPaused
	case string(DownloadComplete):
		return DownloadComplete
	case string(DownloadError):
		return DownloadError
	case string(DownloadRemoved):
		return DownloadRemoved
	default:
		return DownloadUnknown
	}
}

// DownloadTransition permits recovery/retry of nonterminal tasks. A completed
// or removed execution cannot become live again: a retry needs a new task.
func DownloadTransition(from, to DownloadState) bool {
	if from == to {
		return true
	}
	if to == DownloadUnknown {
		return false
	}
	if from == DownloadRemoved {
		return false
	}
	if from == DownloadComplete {
		return to == DownloadRemoved
	}
	return true
}
