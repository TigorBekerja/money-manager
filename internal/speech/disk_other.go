//go:build !windows

package speech

func freeSpace(string) int64 { return -1 }
