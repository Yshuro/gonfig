//go:build unix

package output

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readOnlyBindMount simulates a file on a read-only bind mount as seen by a user without write permission: opening it
// for writing and checking its write access fail with EACCES, and its mount is read-only if mounted is set
func readOnlyBindMount(t *testing.T, file string, mounted bool) {
	t.Helper()
	openWritable = func(name string) (*os.File, error) {
		if name == file {
			return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
		}
		return openWritableFile(name)
	}
	modeChangeable = func(name string) error {
		if name == file {
			return &fs.PathError{Op: "access", Path: name, Err: syscall.EACCES}
		}
		return checkModeChangeable(name)
	}
	readOnlyMount = func(name string) bool { return mounted && name == file }
	t.Cleanup(func() {
		openWritable, modeChangeable, readOnlyMount = openWritableFile, checkModeChangeable, isReadOnlyMount
	})
}

// TestWriteAllPermissionErrorOnReadOnlyMount checks that a permission error of a file on a read-only mount is a
// read-only error, for a file to write and for the mode change of an unchanged file: it fails before any file is
// changed, or is skipped with a warning with IgnoreReadOnly
func TestWriteAllPermissionErrorOnReadOnlyMount(t *testing.T) {
	for _, tt := range []struct {
		name    string
		data    string
		force   bool
		wantErr string
		wantMsg string
	}{
		{name: "content", data: "b: 2\n", wantErr: "failed to write %s, no file has been changed: open %s: read-only file system",
			wantMsg: `"msg":"Skipping read-only file","path":"%s","error":"read-only file system"`},
		{name: "mode", data: "b: 1\n", force: true, wantErr: "failed to change the mode of %s, no file has been changed: access %s: read-only file system",
			wantMsg: `"msg":"Skipping mode change of read-only file","path":"%s","mode":"0600","error":"read-only file system"`},
	} {
		for _, ignore := range []bool{true, false} {
			t.Run(tt.name+map[bool]string{true: " ignored", false: " failing"}[ignore], func(t *testing.T) {
				logs := captureLogs(t)
				dir := resolvedTempDir(t)
				other := filepath.Join(dir, "a.yaml")
				mounted := filepath.Join(dir, "b.yaml")
				otherInfo := writeOld(t, other, "a: ${A}\n")
				mountedInfo := writeOld(t, mounted, "b: 1\n")
				readOnlyBindMount(t, mounted, true)

				opts := inlineOptions
				opts.ForceMode, opts.IgnoreReadOnly = tt.force, ignore
				err := WriteAll([]File{
					{Path: other, Data: []byte("a: made-up-secret\n"), Options: opts},
					{Path: mounted, Data: []byte(tt.data), Options: opts},
				})
				if ignore {
					require.NoError(t, err)
					content, err := os.ReadFile(other)
					require.NoError(t, err)
					assert.Equal(t, "a: made-up-secret\n", string(content))
					assert.Contains(t, logs.String(), fmt.Sprintf(tt.wantMsg, mounted))
					assert.NotContains(t, logs.String(), "made-up-secret")
				} else {
					assert.ErrorIs(t, err, syscall.EROFS)
					assert.EqualError(t, err, fmt.Sprintf(tt.wantErr, mounted, mounted))
					assertUntouched(t, other, "a: ${A}\n", otherInfo)
				}
				assertUntouched(t, mounted, "b: 1\n", mountedInfo)
				assertNoLeftovers(t, dir)
			})
		}
	}
}

// TestWriteAllPermissionErrorOnWritableMount checks that permission errors of the checks on a writable mount are
// ignored as before, so a file without write permission is still replaced and the mode of an unchanged one changed
func TestWriteAllPermissionErrorOnWritableMount(t *testing.T) {
	dir := resolvedTempDir(t)
	changed := filepath.Join(dir, "a.yaml")
	unchanged := filepath.Join(dir, "b.yaml")
	writeOld(t, changed, "a: ${A}\n")
	writeOld(t, unchanged, "b: 1\n")
	readOnlyBindMount(t, changed, false)

	opts := inlineOptions
	require.NoError(t, WriteAll([]File{{Path: changed, Data: []byte("a: 1\n"), Options: opts}}))
	content, err := os.ReadFile(changed)
	require.NoError(t, err)
	assert.Equal(t, "a: 1\n", string(content))

	readOnlyBindMount(t, unchanged, false)
	opts.ForceMode = true
	require.NoError(t, WriteAll([]File{{Path: unchanged, Data: []byte("b: 1\n"), Options: opts}}))
	assertMode(t, unchanged, 0o600)
}

// TestWriteAllPermissionErrorInPlaceOnReadOnlyMount checks that a permission error writing in place on a read-only
// mount, e.g. after the checks passed, is skipped with IgnoreReadOnly instead of failing
func TestWriteAllPermissionErrorInPlaceOnReadOnlyMount(t *testing.T) {
	logs := captureLogs(t)
	dir := resolvedTempDir(t)
	path := filepath.Join(dir, "a.yaml")
	info := writeOld(t, path, "a: ${A}\n")
	rename = func(_, _ string) error { return syscall.EBUSY }
	writeFile = func(name string, _ []byte, _ fs.FileMode) error {
		return &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
	}
	readOnlyMount = func(name string) bool { return name == path }
	t.Cleanup(func() { rename, writeFile, readOnlyMount = os.Rename, os.WriteFile, isReadOnlyMount })

	opts := inlineOptions
	opts.IgnoreReadOnly = true
	require.NoError(t, WriteAll([]File{{Path: path, Data: []byte("a: 1\n"), Options: opts}}))
	assert.Contains(t, logs.String(), `"msg":"Skipping read-only file","path":"`+path+`","error":"read-only file system"`)
	assertUntouched(t, path, "a: ${A}\n", info)
}

// TestAsReadOnlyErr checks that only permission errors of files on read-only mounts become read-only errors,
// keeping their operation and path
func TestAsReadOnlyErr(t *testing.T) {
	readOnlyMount = func(name string) bool { return name == "ro" }
	t.Cleanup(func() { readOnlyMount = isReadOnlyMount })

	assert.EqualError(t, asReadOnlyErr(&fs.PathError{Op: "open", Path: "ro", Err: syscall.EACCES}), "open ro: read-only file system")
	assert.EqualError(t, asReadOnlyErr(&fs.PathError{Op: "chmod", Path: "ro", Err: syscall.EPERM}), "chmod ro: read-only file system")
	assert.EqualError(t, asReadOnlyErr(&fs.PathError{Op: "open", Path: "rw", Err: syscall.EACCES}), "open rw: permission denied")
	assert.EqualError(t, asReadOnlyErr(&fs.PathError{Op: "open", Path: "ro", Err: syscall.ENOENT}), "open ro: no such file or directory")
	assert.NoError(t, asReadOnlyErr(nil))
}

// TestIsReadOnlyMount checks that a writable temporary directory and a missing file are no read-only mount
func TestIsReadOnlyMount(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, isReadOnlyMount(dir))
	assert.False(t, isReadOnlyMount(filepath.Join(dir, "missing")))
}
