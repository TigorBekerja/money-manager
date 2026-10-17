//go:build !windows

package main

import "os"

func enableVirtualTerminal() bool { return true }

// wakeConsole is not supported here; voice "stop" then asks for Enter.
func wakeConsole(*os.File) func() { return nil }
