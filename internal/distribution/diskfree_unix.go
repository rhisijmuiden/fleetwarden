//go:build unix

package distribution

import "syscall"

// diskSpace reports the free and total space of the disk holding dir.
func diskSpace(dir string) (free, total uint64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, false
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), true //nolint:unconvert,gosec // field types differ per platform
}
