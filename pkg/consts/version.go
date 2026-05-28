package consts

// vars below are set by '-X' flag
var (
	Version    = "dev"
	Commit     = "unknown"
	CommitDate = "unknown"
	GOARM      = "" // arm variant (5, 6, or 7); empty on non-arm builds
)
