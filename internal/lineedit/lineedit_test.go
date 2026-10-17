package lineedit

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// suggestCommands is a tiny stand-in for the app's suggester.
func suggestCommands(line string) Suggestions {
	if line == "" || strings.Contains(line, " ") {
		if line == "income " {
			return Suggestions{Hint: "<amount>"}
		}
		return Suggestions{}
	}
	var items []Item
	for _, c := range []Item{
		{Line: "income ", Label: "income"},
		{Line: "expenses ", Label: "expenses"},
		{Line: "exit", Label: "/exit", Submit: true},
		{Line: "help", Label: "/help", Submit: true},
	} {
		if strings.HasPrefix(c.Line, strings.ToLower(line)) && strings.TrimSpace(c.Line) != line {
			items = append(items, c)
		}
	}
	return Suggestions{Items: items}
}

func newEditor(input string) (*Editor, *bytes.Buffer) {
	var out bytes.Buffer
	return &Editor{In: strings.NewReader(input), Out: &out, Suggest: suggestCommands, Color: true}, &out
}

func readAll(t *testing.T, e *Editor) []string {
	t.Helper()
	var lines []string
	for {
		line, err := e.ReadLine("> ", true)
		if errors.Is(err, io.EOF) {
			return lines
		}
		if err != nil {
			t.Fatalf("ReadLine: %v", err)
		}
		lines = append(lines, line)
	}
}

func TestReadLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"plain", "hello\r", []string{"hello"}},
		{"tab completes", "in\t5000\r", []string{"income 5000"}},
		{"tab is case-insensitive", "IN\t1\r", []string{"income 1"}},
		{"enter accepts and keeps editing for commands with args", "in\r5000\r", []string{"income 5000"}},
		{"enter accepts and submits for commands without args", "he\r", []string{"help"}},
		{"down selects next item", "e\x1b[B\t\r", []string{"exit"}},
		{"up wraps around", "e\x1b[A\x1b[A\t\r", []string{"expenses "}},
		{"right arrow at end accepts", "in\x1b[C1\r", []string{"income 1"}},
		{"esc dismisses menu", "in\x1b\r", []string{"in"}},
		{"no match, enter submits as typed", "xyz\r", []string{"xyz"}},
		{"backspace", "ab\x7fc\r", []string{"ac"}},
		{"ctrl+h backspace", "ab\x08c\r", []string{"ac"}},
		{"left and insert", "abc\x1b[D\x1b[DX\r", []string{"aXbc"}},
		{"home and end", "bc\x1b[Ha\x1b[Fd\r", []string{"abcd"}},
		{"delete key", "abc\x1b[H\x1b[3~\r", []string{"bc"}},
		{"ctrl+u clears", "abc\x15d\r", []string{"d"}},
		{"crlf counts once", "a\r\nb\r", []string{"a", "b"}},
		{"history up", "income 1\r\x1b[A\r", []string{"income 1", "income 1"}},
		{"history up then down restores draft", "one\rtwo\x1b[A\x1b[B\r", []string{"one", "two"}},
		{"unknown escape ignored", "a\x1b[1;5Cb\r", []string{"ab"}},
		{"unicode", "kopi ☕\r", []string{"kopi ☕"}},
		{"eof mid-line returns the line", "abc", []string{"abc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newEditor(tt.input)
			got := readAll(t, e)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCtrlCAndCtrlD(t *testing.T) {
	e, _ := newEditor("ab\x03")
	if _, err := e.ReadLine("> ", true); !errors.Is(err, ErrInterrupt) {
		t.Errorf("Ctrl+C: err = %v, want ErrInterrupt", err)
	}
	e, _ = newEditor("\x04")
	if _, err := e.ReadLine("> ", true); !errors.Is(err, io.EOF) {
		t.Errorf("Ctrl+D: err = %v, want io.EOF", err)
	}
	e, _ = newEditor("\x1a")
	if _, err := e.ReadLine("> ", true); !errors.Is(err, io.EOF) {
		t.Errorf("Ctrl+Z: err = %v, want io.EOF", err)
	}
}

func TestNoCompletionMode(t *testing.T) {
	e, out := newEditor("in\t\r")
	line, err := e.ReadLine("Month: ", false)
	if err != nil || line != "in" {
		t.Errorf("got %q, %v; want \"in\" with completion off", line, err)
	}
	if strings.Contains(out.String(), "income") {
		t.Error("menu rendered with completion off")
	}
	e.In = strings.NewReader("\x1b[A\r")
	e.r = nil
	if line, _ := e.ReadLine("Month: ", false); line != "" {
		t.Errorf("history used with completion off: %q", line)
	}
}

func TestRenderShowsGhostMenuAndHint(t *testing.T) {
	e, out := newEditor("in")
	e.ReadLine("> ", true)
	s := out.String()
	if !strings.Contains(s, "\x1b[2mcome\x1b[0m") {
		t.Errorf("ghost text missing: %q", s)
	}
	if !strings.Contains(s, "› income") {
		t.Errorf("menu missing: %q", s)
	}

	e, out = newEditor("income ")
	e.ReadLine("> ", true)
	if !strings.Contains(out.String(), "\x1b[2m<amount>\x1b[0m") {
		t.Errorf("hint missing: %q", out.String())
	}

	// Without color, no ghost text (it would look like typed text).
	e, out = newEditor("in")
	e.Color = false
	e.ReadLine("> ", true)
	if strings.Contains(out.String(), "incomecome") || strings.Contains(out.String(), "> income") {
		t.Errorf("ghost shown without color: %q", out.String())
	}
}

func TestReadKey(t *testing.T) {
	e, _ := newEditor("x\r\n\x1b[A\x1b\x03\x04")
	var raw int
	e.Raw = func() (func(), error) { raw++; return func() {}, nil }
	var got []string
	for {
		k, err := e.ReadKey()
		got = append(got, k)
		if err != nil {
			break
		}
	}
	if strings.Join(got, "|") != "x|enter||esc|ctrl+c|eof" {
		t.Errorf("keys = %q", got)
	}
	if raw != len(got) {
		t.Errorf("raw mode entered %d times for %d keys", raw, len(got))
	}
}

func TestWakeRune(t *testing.T) {
	e, _ := newEditor(string(WakeRune) + "a" + string(WakeRune) + "b\r")
	if k, _ := e.ReadKey(); k != "wake" {
		t.Errorf("ReadKey = %q, want wake", k)
	}
	if line, _ := e.ReadLine("> ", false); line != "ab" {
		t.Errorf("ReadLine = %q, want the wake character ignored", line)
	}
	if e.Wake() {
		t.Error("Wake() = true without WakeFn")
	}
	called := false
	e.WakeFn = func() { called = true }
	if !e.Wake() || !called {
		t.Error("Wake did not call WakeFn")
	}
}

func TestRawModeRestored(t *testing.T) {
	var raw, restored int
	e, _ := newEditor("a\rb\r")
	e.Raw = func() (func(), error) { raw++; return func() { restored++ }, nil }
	readAll(t, e)
	if raw != 3 || restored != 3 { // two lines + the EOF read
		t.Errorf("raw=%d restored=%d, want 3 each", raw, restored)
	}
}
