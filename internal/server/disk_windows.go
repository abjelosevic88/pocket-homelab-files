//go:build windows

package server

// diskFree is not measured on Windows.
func diskFree(string) (free, total uint64) { return 0, 0 }
