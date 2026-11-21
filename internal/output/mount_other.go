//go:build unix && !linux && !darwin && !freebsd

package output

// isReadOnlyMount returns false, since mounts are not checked on other Unix systems, e.g. OpenBSD, which report
// read-only file systems with EROFS only
func isReadOnlyMount(_ string) bool {
	return false
}
