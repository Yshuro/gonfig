package output

import "syscall"

// stRdonly is ST_RDONLY of statfs(2), which Linux sets in the flags of read-only file systems and mounts since 2.6.36
const stRdonly = 1

// isReadOnlyMount returns whether the file is on a read-only mount, including a read-only bind mount.
// It returns false if the mount cannot be checked, so the file is written as before.
func isReadOnlyMount(path string) bool {
	var st syscall.Statfs_t
	return syscall.Statfs(path, &st) == nil && st.Flags&stRdonly != 0
}
