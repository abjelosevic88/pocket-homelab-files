package vfs

import "io/fs"

// linkKey: hardlinks are not told apart on Windows; every file counts.
func linkKey(fs.FileInfo) ([2]uint64, bool) { return [2]uint64{}, false }
