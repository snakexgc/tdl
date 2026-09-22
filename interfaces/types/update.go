package types

import "time"

type UpdateInfo struct {
	CurrentVersion  string          `json:"current_version"`
	CurrentCommit   string          `json:"current_commit"`
	CurrentDate     string          `json:"current_date"`
	GOOS            string          `json:"goos"`
	GOARCH          string          `json:"goarch"`
	Repository      string          `json:"repository"`
	Runtime         string          `json:"runtime"`
	Docker          bool            `json:"docker"`
	LatestVersion   string          `json:"latest_version"`
	LatestName      string          `json:"latest_name"`
	LatestURL       string          `json:"latest_url"`
	ReleaseNotes    string          `json:"release_notes"`
	PublishedAt     time.Time       `json:"published_at,omitempty"`
	AssetName       string          `json:"asset_name,omitempty"`
	AssetURL        string          `json:"asset_url,omitempty"`
	NeedsUpdate     bool            `json:"needs_update"`
	CanUpdate       bool            `json:"can_update"`
	Message         string          `json:"message"`
	StableReleases  []UpdateRelease `json:"stable_releases,omitempty"`
	PreviewReleases []UpdateRelease `json:"preview_releases,omitempty"`
}

// UpdateRelease describes a published version that can be selected independently
// of whether it is newer than the running binary.
type UpdateRelease struct {
	Version      string `json:"version"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	ReleaseNotes string `json:"release_notes"`
	// ReleaseNotesHTML contains safe, server-rendered Markdown, never raw release HTML.
	ReleaseNotesHTML string    `json:"release_notes_html,omitempty"`
	PublishedAt      time.Time `json:"published_at"`
	Prerelease       bool      `json:"prerelease"`
	Current          bool      `json:"current"`
	CanInstall       bool      `json:"can_install"`
	AssetName        string    `json:"asset_name,omitempty"`
	Message          string    `json:"message"`
}

type UpdatePlan struct {
	SourcePath string `json:"source_path"`
	Version    string `json:"version"`
	AssetName  string `json:"asset_name"`
}
