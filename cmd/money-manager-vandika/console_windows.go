//go:build windows

package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"money-manager/internal/lineedit"
)

var procWriteConsoleInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")

// inputRecord mirrors INPUT_RECORD holding a KEY_EVENT_RECORD.
type inputRecord struct {
	EventType       uint16
	_               uint16
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

// wakeConsole returns a function that types lineedit.WakeRune into this console's input,
// releasing a blocked key read (used when voice input hears "stop").
func wakeConsole(f *os.File) func() {
	return func() {
		recs := [2]inputRecord{
			{EventType: 1, KeyDown: 1, RepeatCount: 1, UnicodeChar: uint16(lineedit.WakeRune)},
			{EventType: 1, KeyDown: 0, RepeatCount: 1, UnicodeChar: uint16(lineedit.WakeRune)},
		}
		var written uint32
		procWriteConsoleInput.Call(f.Fd(), uintptr(unsafe.Pointer(&recs[0])), uintptr(len(recs)),
			uintptr(unsafe.Pointer(&written)))
	}
}

// enableVirtualTerminal turns on ANSI escape processing so colors work in legacy cmd.
func enableVirtualTerminal() bool {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
