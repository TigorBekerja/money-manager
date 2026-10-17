// Package lineedit is a small single-line editor for raw-mode terminals: cursor movement,
// history, a suggestion menu with inline ghost text, and Tab completion.
package lineedit

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// ErrInterrupt is returned when the user presses Ctrl+C.
var ErrInterrupt = errors.New("interrupted")

// WakeRune is injected into the terminal input by Wake to release a pending ReadKey.
// It is a private-use character that can't be typed, and ReadLine ignores it.
const WakeRune = '\uE000'

// Suggestions describes what to offer for the current input line.
type Suggestions struct {
	Items []Item // menu entries; the selected one is shown as ghost text and accepted by Tab
	Hint  string // dim text after the input when there are no items, e.g. "<amount>"
}

type Item struct {
	Line   string // the whole input line after accepting this item
	Label  string // shown in the menu
	Desc   string // shown next to the label
	Submit bool   // Enter accepts and submits; otherwise Enter accepts and keeps editing
}

type Editor struct {
	In      io.Reader
	Out     io.Writer
	Raw     func() (restore func(), err error) // puts the terminal in raw mode; nil in tests
	Suggest func(line string) Suggestions
	Color   bool   // dim/bold styling; ghost text is only shown with color
	WakeFn  func() // injects WakeRune into the terminal input; nil if unsupported

	r       *bufio.Reader
	history []string
}

type state struct {
	prompt    string
	buf       []rune
	pos       int
	sel       int
	dismissed bool // Esc hides the menu until the next edit
	complete  bool // suggestions and history enabled for this line
	histIdx   int
	draft     []rune
}

// ReadLine reads one line. With complete set, suggestions and history are active and the
// line is added to history. It returns io.EOF on Ctrl+D/Ctrl+Z on an empty line or end of
// input, and ErrInterrupt on Ctrl+C.
func (e *Editor) ReadLine(prompt string, complete bool) (string, error) {
	if e.r == nil {
		e.r = bufio.NewReader(e.In)
	}
	if e.Raw != nil {
		restore, err := e.Raw()
		if err != nil {
			return "", err
		}
		defer restore()
	}

	s := &state{prompt: prompt, complete: complete, histIdx: len(e.history)}
	e.render(s, false)
	for {
		r, _, err := e.r.ReadRune()
		if err != nil {
			e.render(s, true)
			io.WriteString(e.Out, "\r\n")
			if err == io.EOF && len(s.buf) > 0 {
				return string(s.buf), nil
			}
			return "", err
		}

		edited := true
		switch r {
		case '\r', '\n':
			if r == '\r' && e.r.Buffered() > 0 {
				if next, _, _ := e.r.ReadRune(); next != '\n' {
					e.r.UnreadRune()
				}
			}
			if it, ok := e.selected(s); ok && e.accept(s, it) && !it.Submit {
				break
			}
			e.render(s, true)
			io.WriteString(e.Out, "\r\n")
			line := string(s.buf)
			if complete {
				e.remember(line)
			}
			return line, nil
		case '\t':
			if it, ok := e.selected(s); ok {
				e.accept(s, it)
			}
		case 3: // Ctrl+C
			e.render(s, true)
			io.WriteString(e.Out, "\r\n")
			return "", ErrInterrupt
		case 4, 0x1a: // Ctrl+D, Ctrl+Z
			if len(s.buf) == 0 {
				e.render(s, true)
				io.WriteString(e.Out, "\r\n")
				return "", io.EOF
			}
			s.deleteAt(s.pos)
		case 0x7f, 0x08: // Backspace
			if s.pos > 0 {
				s.pos--
				s.deleteAt(s.pos)
			}
		case 0x15: // Ctrl+U
			s.buf, s.pos = s.buf[:0], 0
		case 0x01: // Ctrl+A
			s.pos, edited = 0, false
		case 0x05: // Ctrl+E
			s.pos, edited = len(s.buf), false
		case 0x1b:
			edited = e.escape(s)
		default:
			if r < 0x20 || r == WakeRune {
				edited = false
				break
			}
			s.buf = append(s.buf[:s.pos], append([]rune{r}, s.buf[s.pos:]...)...)
			s.pos++
		}
		if edited {
			s.sel, s.dismissed = 0, false
		}
		e.render(s, false)
	}
}

// Wake releases a pending ReadKey, which then returns "wake". It reports whether the
// terminal supports it.
func (e *Editor) Wake() bool {
	if e.WakeFn == nil {
		return false
	}
	e.WakeFn()
	return true
}

// ReadKey waits for one key press in raw mode and returns "enter", "esc", "ctrl+c",
// "eof", "wake" (see Wake), or the typed character. Arrow keys and other sequences
// return "".
func (e *Editor) ReadKey() (string, error) {
	if e.r == nil {
		e.r = bufio.NewReader(e.In)
	}
	if e.Raw != nil {
		restore, err := e.Raw()
		if err != nil {
			return "", err
		}
		defer restore()
	}
	r, _, err := e.r.ReadRune()
	if err != nil {
		return "eof", err
	}
	switch r {
	case '\r', '\n':
		if r == '\r' && e.r.Buffered() > 0 {
			if next, _, _ := e.r.ReadRune(); next != '\n' {
				e.r.UnreadRune()
			}
		}
		return "enter", nil
	case 3:
		return "ctrl+c", nil
	case WakeRune:
		return "wake", nil
	case 4, 0x1a:
		return "eof", io.EOF
	case 0x1b:
		if e.r.Buffered() == 0 {
			return "esc", nil
		}
		if next, _, _ := e.r.ReadRune(); next != '[' && next != 'O' {
			e.r.UnreadRune()
			return "esc", nil
		}
		for e.r.Buffered() > 0 {
			if c, _, _ := e.r.ReadRune(); c >= 0x40 && c <= 0x7e {
				break
			}
		}
		return "", nil
	}
	return string(r), nil
}

// escape handles an escape sequence. It reports whether the line was edited.
func (e *Editor) escape(s *state) bool {
	if e.r.Buffered() == 0 {
		s.dismissed = true
		return false
	}
	next, _, err := e.r.ReadRune()
	if err != nil || (next != '[' && next != 'O') {
		e.r.UnreadRune()
		s.dismissed = true
		return false
	}
	var seq strings.Builder
	for {
		c, _, err := e.r.ReadRune()
		if err != nil {
			return false
		}
		seq.WriteRune(c)
		if c >= 0x40 && c <= 0x7e {
			break
		}
	}

	items := e.menu(s)
	switch seq.String() {
	case "A": // Up
		if len(items) > 0 {
			s.sel = (s.sel + len(items) - 1) % len(items)
		} else {
			e.historyMove(s, -1)
		}
	case "B": // Down
		if len(items) > 0 {
			s.sel = (s.sel + 1) % len(items)
		} else {
			e.historyMove(s, 1)
		}
	case "C": // Right; at end of line it accepts the ghost text
		if s.pos < len(s.buf) {
			s.pos++
		} else if it, ok := e.selected(s); ok {
			e.accept(s, it)
			return true
		}
	case "D": // Left
		s.pos = max(0, s.pos-1)
	case "H", "1~", "7~":
		s.pos = 0
	case "F", "4~", "8~":
		s.pos = len(s.buf)
	case "3~": // Delete
		s.deleteAt(s.pos)
		return true
	}
	return false
}

func (s *state) deleteAt(i int) {
	if i < len(s.buf) {
		s.buf = append(s.buf[:i], s.buf[i+1:]...)
	}
}

func (e *Editor) suggestions(s *state) Suggestions {
	if !s.complete || e.Suggest == nil {
		return Suggestions{}
	}
	return e.Suggest(string(s.buf))
}

func (e *Editor) menu(s *state) []Item {
	if s.dismissed {
		return nil
	}
	return e.suggestions(s).Items
}

func (e *Editor) selected(s *state) (Item, bool) {
	items := e.menu(s)
	if len(items) == 0 {
		return Item{}, false
	}
	return items[min(s.sel, len(items)-1)], true
}

// accept replaces the line with the item. It reports whether anything changed.
func (e *Editor) accept(s *state, it Item) bool {
	if string(s.buf) == it.Line {
		return false
	}
	s.buf = []rune(it.Line)
	s.pos = len(s.buf)
	return true
}

func (e *Editor) remember(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	if n := len(e.history); n > 0 && e.history[n-1] == line {
		return
	}
	e.history = append(e.history, line)
}

func (e *Editor) historyMove(s *state, delta int) {
	if !s.complete {
		return
	}
	i := s.histIdx + delta
	if i < 0 || i > len(e.history) {
		return
	}
	if s.histIdx == len(e.history) {
		s.draft = append([]rune(nil), s.buf...)
	}
	s.histIdx = i
	if i == len(e.history) {
		s.buf = append([]rune(nil), s.draft...)
	} else {
		s.buf = []rune(e.history[i])
	}
	s.pos = len(s.buf)
}

func (e *Editor) style(code, text string) string {
	if !e.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// render redraws the input line and, unless final, the ghost text and menu below it.
// The cursor is left on the input line.
func (e *Editor) render(s *state, final bool) {
	var b strings.Builder
	b.WriteString("\r\x1b[J")
	b.WriteString(s.prompt)
	b.WriteString(string(s.buf))

	var items []Item
	if !final {
		items = e.menu(s)
		atEnd := s.pos == len(s.buf)
		if it, ok := e.selected(s); ok && atEnd && e.Color {
			line := string(s.buf)
			if len(it.Line) >= len(line) && strings.EqualFold(it.Line[:len(line)], line) {
				b.WriteString(e.style("2", strings.TrimRight(it.Line[len(line):], " ")))
			}
		} else if hint := e.suggestions(s).Hint; hint != "" && atEnd && e.Color {
			b.WriteString(e.style("2", hint))
		}

		labelW := 0
		for _, it := range items {
			labelW = max(labelW, utf8.RuneCountInString(it.Label))
		}
		for i, it := range items {
			label := it.Label + strings.Repeat(" ", labelW-utf8.RuneCountInString(it.Label))
			if i == min(s.sel, len(items)-1) {
				b.WriteString("\r\n    " + e.style("1", "› "+label) + "   " + it.Desc)
			} else {
				b.WriteString("\r\n    " + e.style("2", "  "+label+"   "+it.Desc))
			}
		}
	}

	if len(items) > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", len(items))
	}
	b.WriteString("\r")
	if col := utf8.RuneCountInString(s.prompt) + s.pos; col > 0 {
		fmt.Fprintf(&b, "\x1b[%dC", col)
	}
	io.WriteString(e.Out, b.String())
}
