//go:build windows

package main

import (
	"os"
	"testing"
	"time"

	"money-manager/internal/lineedit"
)

// TestWakeConsole checks that wakeConsole releases a raw-mode ReadKey in a real console.
// It needs its own console window, so it's opt-in:
//
//	go test -c -o wake.test.exe ./cmd/money-manager-vandika
//	start /min wake.test.exe -test.run TestWakeConsole   (with MM_TEST_CONSOLE_OUT=result.txt)
func TestWakeConsole(t *testing.T) {
	out := os.Getenv("MM_TEST_CONSOLE_OUT")
	if out == "" {
		t.Skip("run in a console window with MM_TEST_CONSOLE_OUT set")
	}
	e := &lineedit.Editor{In: os.Stdin, Out: os.Stdout, Raw: rawMode(os.Stdin), WakeFn: wakeConsole(os.Stdin)}
	got := make(chan string, 1)
	go func() {
		k, err := e.ReadKey()
		if err != nil {
			k = "error: " + err.Error()
		}
		got <- k
	}()
	time.Sleep(300 * time.Millisecond)
	e.Wake()
	result := "timeout"
	select {
	case result = <-got:
	case <-time.After(3 * time.Second):
	}
	os.WriteFile(out, []byte(result), 0o644)
	if result != "wake" {
		t.Errorf("ReadKey = %q, want wake", result)
	}
}
