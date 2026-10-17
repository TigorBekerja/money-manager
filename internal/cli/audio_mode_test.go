package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"money-manager/internal/store"
	"money-manager/internal/voice"
)

// scriptLines reads scripted input. ReadKey waits until the current fake listening
// session has delivered all its events, so key presses happen after the speech.
type scriptLines struct {
	*scannerReader
	mu       sync.Mutex
	gate     chan struct{}
	wake     chan struct{}
	noWake   bool // behave like a terminal that can't be woken
	wakeUsed int
}

func (s *scriptLines) Wake() bool {
	if s.noWake {
		return false
	}
	s.mu.Lock()
	s.wakeUsed++
	s.mu.Unlock()
	s.wake <- struct{}{}
	return true
}

func (s *scriptLines) setGate(g chan struct{}) {
	s.mu.Lock()
	s.gate = g
	s.mu.Unlock()
}

func (s *scriptLines) ReadKey() (string, error) {
	s.mu.Lock()
	g := s.gate
	s.mu.Unlock()
	if g != nil {
		select {
		case <-g:
		case <-s.wake:
			return "wake", nil
		}
	}
	return s.scannerReader.ReadKey()
}

type fakeVoice struct {
	err     error
	engine  *fakeEngine
	started int
}

func (f *fakeVoice) Start(context.Context) (voice.Engine, error) {
	f.started++
	if f.err != nil {
		return nil, f.err
	}
	return f.engine, nil
}

type fakeEngine struct {
	lines   *scriptLines
	scripts [][]voice.Event // one per Listen
	listens int
	closed  bool
}

func (e *fakeEngine) GPU() bool    { return true }
func (e *fakeEngine) Close() error { e.closed = true; return nil }

func (e *fakeEngine) Listen(ctx context.Context) (voice.Listening, error) {
	if e.listens >= len(e.scripts) {
		return nil, errors.New("no more scripts")
	}
	script := e.scripts[e.listens]
	e.listens++
	l := &fakeListening{events: make(chan voice.Event), stop: make(chan struct{}), done: make(chan struct{})}
	gate := make(chan struct{})
	e.lines.setGate(gate)
	go func() {
		defer close(l.done)
		defer close(l.events)
		for _, ev := range script {
			select {
			case l.events <- ev:
			case <-l.stop:
				close(gate)
				return
			}
		}
		// After a spoken stop, the next key must come from Wake (or, without Wake
		// support, from the input after listening finished).
		endsWithStop := len(script) > 0 && script[len(script)-1].Kind == voice.Heard &&
			voice.IsStop(script[len(script)-1].Text)
		if !endsWithStop {
			close(gate)
		}
		<-l.stop
		if endsWithStop && e.lines.noWake {
			close(gate)
		}
	}()
	return l, nil
}

type fakeListening struct {
	events chan voice.Event
	stop   chan struct{}
	once   sync.Once
	done   chan struct{}
}

func (l *fakeListening) Events() <-chan voice.Event { return l.events }
func (l *fakeListening) Finish()                    { l.once.Do(func() { close(l.stop) }) }
func (l *fakeListening) Close()                     { l.Finish(); <-l.done }

var (
	at1 = time.Date(2026, 10, 17, 14, 32, 5, 0, wib)
	at2 = time.Date(2026, 10, 17, 14, 33, 10, 0, wib)
)

func speechScript(heard ...string) []voice.Event {
	evs := []voice.Event{{Kind: voice.Calibrating}, {Kind: voice.Ready}, {Kind: voice.Level, Level: 0.4}}
	for i, h := range heard {
		at := at1.Add(time.Duration(i) * time.Minute)
		evs = append(evs,
			voice.Event{Kind: voice.Recording, At: at},
			voice.Event{Kind: voice.Ready},
			voice.Event{Kind: voice.Understanding, At: at},
			voice.Event{Kind: voice.Heard, At: at, Text: h})
	}
	return evs
}

type audioRun struct {
	out   string
	mem   *store.Memory
	voice *fakeVoice
}

func runVoice(t *testing.T, st Store, scripts [][]voice.Event, input string) audioRun {
	t.Helper()
	var out bytes.Buffer
	lines := &scriptLines{scannerReader: &scannerReader{sc: bufio.NewScanner(strings.NewReader(input)), out: &out},
		wake: make(chan struct{}, 1), noWake: noWakeNext}
	noWakeNext = false
	fv := &fakeVoice{engine: &fakeEngine{lines: lines, scripts: scripts}}
	mem, _ := st.(*store.Memory)
	if st == nil {
		mem = store.NewMemory()
		st = mem
	}
	r := &REPL{Lines: lines, Out: &out, Store: st, Now: func() time.Time { return fixedNow }, Voice: fv}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return audioRun{out: out.String(), mem: mem, voice: fv}
}

func listAll(t *testing.T, mem *store.Memory) []store.Transaction {
	t.Helper()
	txs, err := mem.ListBetween(context.Background(), time.Date(2000, 1, 1, 0, 0, 0, 0, wib), time.Date(2100, 1, 1, 0, 0, 0, 0, wib))
	if err != nil {
		t.Fatal(err)
	}
	return txs
}

func TestAudioRecordEditAndSave(t *testing.T) {
	script := speechScript("income dua ratus ribu", "beli kopi", "Pengeluaran lima puluh lima ribu.", "[BLANK_AUDIO]")
	run := runVoice(t, nil, [][]voice.Event{script}, strings.Join([]string{
		"/audio",
		"",                 // key: Enter → review
		"t",                // set times of every entry
		"2026-10-15 09:00", // #1 full date and time
		"25:00",            // #2 invalid → asked again
		"",                 // #2 keep
		"y",
		"list 10 2026",
		"/exit",
	}, "\n"))
	out := run.out

	assertContains(t, out,
		"Starting speech model... ready (GPU).",
		`Say one entry, then pause. Example: "income dua ratus ribu"`,
		`Enter or say "stop" / "berhenti": finish and review · Esc: cancel`,
		"Ready for entry #1 · speak now",
		"Recording entry #1...",
		"understanding 1...",
		`✓ #1   income      200,000.00 · 14:32   heard "income dua ratus ribu"`,
		`✗     heard "beli kopi": no income/expenses word`,
		`✓ #2   expense      55,000.00 · 14:34   heard "Pengeluaran lima puluh lima ribu."`,
		"Recorded entries (NOT saved yet)",
		"1   income dua ratus ribu",
		"Income 200,000.00 · Expenses 55,000.00",
		"Date & time is when each entry was recorded",
		"t: set date & time of every entry · t <#>: one entry · d <#>: delete",
		"#1 income      200,000.00    Date & time [2026-10-17 14:32]: ",
		`✗ Invalid time "25:00"`,
		"2026-10-15 09:00 *",
		"* changed by you",
		"✓ Saved 2 entries. Recordings cleared from memory.",
	)
	if strings.Contains(out, "BLANK") {
		t.Error("blank audio was shown")
	}

	txs := listAll(t, run.mem)
	if len(txs) != 2 {
		t.Fatalf("saved %d rows, want 2", len(txs))
	}
	if txs[0].Kind != store.Income || txs[0].Amount != 20_000_000 || !txs[0].OccurredAt.Equal(time.Date(2026, 10, 15, 9, 0, 0, 0, wib)) {
		t.Errorf("row 1 = %+v", txs[0])
	}
	// Unedited entry keeps the moment recording started (the Heard event's time).
	if txs[1].Kind != store.Expense || txs[1].Amount != 5_500_000 || !txs[1].OccurredAt.Equal(at1.Add(2*time.Minute)) {
		t.Errorf("row 2 = %+v", txs[1])
	}
	if !run.voice.engine.closed {
		t.Error("engine not closed after /audio")
	}
}

func TestAudioNothingSavedBeforeYes(t *testing.T) {
	run := runVoice(t, nil, [][]voice.Event{speechScript("income seribu", "income dua ribu")},
		"/audio\n\nd 3\nd 1\nt 1\n09:15\nn\n\nn\ny\n") // first "n" answered No, second confirmed
	assertContains(t, run.out,
		"✗ Choose an entry number from 1 to 2",
		"✓ Removed #1.",
		"#1 income        2,000.00    Date & time [2026-10-17 14:33]: ",
		"2026-10-17 09:15 *",
		"Discard 1 entry? [y/N]: ",
		"Discarded 1 entry.")
	if n := len(listAll(t, run.mem)); n != 0 {
		t.Errorf("%d rows saved after discarding", n)
	}
}

func TestAudioRecordMore(t *testing.T) {
	run := runVoice(t, nil, [][]voice.Event{speechScript("income seribu"), speechScript("pengeluaran lima ratus")},
		"/audio\n\nr\n\ny\n")
	assertContains(t, run.out, "Listening again.", "Ready for entry #2", "✓ #2", "✓ Saved 2 entries.")
	if n := len(listAll(t, run.mem)); n != 2 {
		t.Errorf("saved %d rows, want 2", n)
	}
	if run.voice.engine.listens != 2 || run.voice.started != 1 {
		t.Errorf("listens=%d starts=%d; the engine should be reused", run.voice.engine.listens, run.voice.started)
	}
}

// noWakeNext makes the next runVoice use a key reader without Wake support.
var noWakeNext bool

func TestAudioVoiceStop(t *testing.T) {
	for _, stop := range []string{"Stop.", "berhenti"} {
		// No Enter key in the input: the spoken stop word ends listening.
		run := runVoice(t, nil, [][]voice.Event{speechScript("income dua ratus ribu", stop)}, "/audio\ny\n")
		assertContains(t, run.out,
			`Enter or say "stop" / "berhenti": finish and review`,
			"✓ #1   income      200,000.00",
			`■ heard "`+stop+`": stopped listening.`,
			"Recorded entries (NOT saved yet)",
			"✓ Saved 1 entry.")
		if strings.Contains(run.out, "no income/expenses word") {
			t.Errorf("%q was treated as an entry", stop)
		}
		if strings.Contains(run.out, "Press Enter to review.") {
			t.Error("asked for Enter although Wake is supported")
		}
		if n := len(listAll(t, run.mem)); n != 1 {
			t.Errorf("saved %d rows, want 1", n)
		}
	}
}

func TestAudioVoiceStopWithoutWake(t *testing.T) {
	noWakeNext = true
	run := runVoice(t, nil, [][]voice.Event{speechScript("income seribu", "stop")}, "/audio\n\ny\n")
	assertContains(t, run.out, "stopped listening.", "Press Enter to review.", "✓ Saved 1 entry.")
}

func TestAudioEscWithPending(t *testing.T) {
	// Esc, then keep the entries (default No to discarding), then save.
	run := runVoice(t, nil, [][]voice.Event{speechScript("income seribu")}, "/audio\nesc\n\ny\n")
	assertContains(t, run.out, "Stop and discard 1 entry? [y/N]: ", "Recorded entries", "✓ Saved 1 entry.")

	run = runVoice(t, nil, [][]voice.Event{speechScript("income seribu")}, "/audio\nesc\ny\n")
	assertContains(t, run.out, "Discarded 1 entry.")
	if n := len(listAll(t, run.mem)); n != 0 {
		t.Errorf("%d rows saved", n)
	}

	run = runVoice(t, nil, [][]voice.Event{speechScript()}, "/audio\nesc\n")
	assertContains(t, run.out, "Stopped listening.")
	run = runVoice(t, nil, [][]voice.Event{speechScript()}, "/audio\n\n")
	assertContains(t, run.out, "No entries recorded.")
}

type failingStore struct {
	*store.Memory
	fails int
}

func (f *failingStore) AddTransactions(ctx context.Context, rows []store.NewTransaction) ([]store.Transaction, error) {
	if f.fails > 0 {
		f.fails--
		return nil, errors.New("connection lost")
	}
	return f.Memory.AddTransactions(ctx, rows)
}

func TestAudioSaveFailureKeepsEntries(t *testing.T) {
	fs := &failingStore{Memory: store.NewMemory(), fails: 1}
	run := runVoice(t, fs, [][]voice.Event{speechScript("income seribu")}, "/audio\n\ny\ny\n")
	assertContains(t, run.out, "✗ Couldn't save: connection lost. Nothing was saved; the entries are still here.", "✓ Saved 1 entry.")
	if n := len(listAll(t, fs.Memory)); n != 1 {
		t.Errorf("saved %d rows, want 1 after retry", n)
	}
}

func TestAudioMicFailure(t *testing.T) {
	script := append(speechScript("income seribu"), voice.Event{Kind: voice.Failed, Fatal: true, Err: errors.New("the microphone stopped")})
	run := runVoice(t, nil, [][]voice.Event{script}, "/audio\nx\ny\n")
	assertContains(t, run.out, "✗ the microphone stopped", "Press any key to continue.", "Recorded entries", "✓ Saved 1 entry.")
}

func TestAudioStartFailure(t *testing.T) {
	var out bytes.Buffer
	lines := &scriptLines{scannerReader: &scannerReader{sc: bufio.NewScanner(strings.NewReader("/audio\n")), out: &out}}
	r := &REPL{Lines: lines, Out: &out, Store: store.NewMemory(), Now: func() time.Time { return fixedNow },
		Voice: &fakeVoice{err: errors.New("Voice input isn't set up yet. Run /setup-audio first.")}}
	r.Run(context.Background())
	assertContains(t, out.String(), "✗ Voice input isn't set up yet. Run /setup-audio first.", "Run /audio-status to see what's missing.")
}

func TestAudioInTutorialAndUnavailable(t *testing.T) {
	out := run(t, store.NewMemory(), "/audio\n/tutorial\n/audio\nq\n")
	assertContains(t, out, "Voice input isn't available in this build.", "That works outside the tutorial.")
}

func TestParseEditTime(t *testing.T) {
	base := time.Date(2026, 10, 17, 14, 32, 0, 0, wib)
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2026-10-15 09:00", time.Date(2026, 10, 15, 9, 0, 0, 0, wib)},
		{"2026-10-15", time.Date(2026, 10, 15, 14, 32, 0, 0, wib)},
		{"09:00", time.Date(2026, 10, 17, 9, 0, 0, 0, wib)},
		{"7:05", time.Date(2026, 10, 17, 7, 5, 0, 0, wib)},
	}
	for _, tt := range tests {
		got, err := ParseEditTime(tt.in, base)
		if err != nil || !got.Equal(tt.want) {
			t.Errorf("ParseEditTime(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"2026-02-30", "25:00", "kemarin", "2026-10-15 9pm"} {
		if _, err := ParseEditTime(bad, base); err == nil {
			t.Errorf("ParseEditTime(%q) accepted", bad)
		}
	}
}
