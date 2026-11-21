//go:build windows

package output

import (
	"errors"
	"io/fs"
	"os"
)

// modeBits are the permission bits os.Chmod applies on Windows: only the owner's write bit, which clears or sets the
// read-only attribute. Modes always read as 0444 or 0666 there, so other bits would make every mode look different.
const modeBits fs.FileMode = 0o200

// isReadOnlyErr returns whether the error is caused by the read-only attribute of the file, which Go reports as a
// missing write permission. Windows has no error of its own for it, so other permission errors of files with the
// attribute are treated as read-only errors as well.
// Check the current mode because the attribute may have changed since the file was prepared.
func isReadOnlyErr(err error, target string) bool {
	if !errors.Is(err, fs.ErrPermission) {
		return false
	}
	info, statErr := os.Stat(target)
	return statErr == nil && info.Mode().Perm()&modeBits == 0
}
