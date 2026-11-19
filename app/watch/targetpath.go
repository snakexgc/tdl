package watch

import "github.com/snakexgc/tdl/rte/targetpath"

func safePathSegment(s string) string                 { return targetpath.SafePathSegment(s) }
func cleanTargetRoot(s string) string                 { return targetpath.CleanTargetRoot(s) }
func joinTargetPath(s string, parts ...string) string { return targetpath.JoinTargetPath(s, parts...) }

func resolveTargetPath(base, name string) (string, string, string) {
	return targetpath.ResolveTargetPath(base, name)
}
