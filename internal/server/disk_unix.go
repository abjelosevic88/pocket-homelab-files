//go:build !windows

package server

import "syscall"

// diskFree reports free and total bytes of the file system holding p.
func diskFree(p string) (free, total uint64) {
	var st syscall.Statfs_t
	if syscall.Statfs(p, &st) != nil {
		return 0, 0
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize)
}
