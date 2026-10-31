//go:build !windows

package vfs

import (
	"io/fs"
	"syscall"
)

// linkKey names the data behind a file with more than one hardlink (device and inode).
func linkKey(fi fs.FileInfo) ([2]uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink < 2 {
		return [2]uint64{}, false
	}
	return [2]uint64{uint64(st.Dev), uint64(st.Ino)}, true
}
