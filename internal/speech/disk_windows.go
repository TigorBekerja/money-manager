//go:build windows

package speech

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// freeSpace returns the free bytes on the drive holding dir (or its nearest existing
// parent), or -1 if unknown.
func freeSpace(dir string) int64 {
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return -1
		}
		dir = parent
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return -1
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return -1
	}
	return int64(free)
}
