//go:build darwin || freebsd

package output

import "syscall"

// mntRdonly is MNT_RDONLY of statfs(2), which is set in the flags of read-only mounts
const mntRdonly = 1

// isReadOnlyMount returns whether the file is on a read-only mount.
// It returns false if the mount cannot be checked, so the file is written as before.
func isReadOnlyMount(path string) bool {
	var st syscall.Statfs_t
	return syscall.Statfs(path, &st) == nil && st.Flags&mntRdonly != 0
}
