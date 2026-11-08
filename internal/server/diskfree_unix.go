//go:build unix

package server

import "syscall"

// freeBytes reports the free space for unprivileged writes on the disk of dir.
func freeBytes(dir string) (uint64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return st.Bavail * uint64(st.Bsize), true //nolint:unconvert,gosec // field types differ per platform
}
