package voice

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"money-manager/internal/audio"
)

// chanSource is a microphone fed by the test.
type chanSource struct {
	frames chan []int16
	once   sync.Once
	closed chan struct{}
	err    error
}

func newChanSource() *chanSource {
	return &chanSource{frames: make(chan []int16, 1024), closed: make(chan struct{})}
}
func (c *chanSource) Frames() <-chan []int16 { return c.frames }
func (c *chanSource) Err() error             { return c.err }
func (c *chanSource) Close() error           { c.once.Do(func() { close(c.closed) }); return nil }

func (c *chanSource) feed(samples []int16) {
	for i := 0; i < len(samples); i += 1600 {
		c.frames <- samples[i:min(i+1600, len(samples))]
	}
}

type fakeTranscriber struct {
	mu     sync.Mutex
	texts  []string
	calls  int
	block  chan struct{}
	prompt string
}

func (f *fakeTranscriber) Transcribe(ctx context.Context, wav []byte, prompt string) (string, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := audio.DecodeWAV(wav); err != nil {
		return "", err
	}
	f.prompt = prompt
	text := f.texts[f.calls%len(f.texts)]
	f.calls++
	return text, nil
}

func quiet(d time.Duration) []int16 { return make([]int16, int(d.Seconds()*audio.SampleRate)) }

func talk(d time.Duration) []int16 {
	s := make([]int16, int(d.Seconds()*audio.SampleRate))
	for i := range s {
		s[i] = int16(8000 * math.Sin(2*math.Pi*220*float64(i)/audio.SampleRate))
	}
	return s
}

var start = time.Date(2026, 10, 17, 14, 32, 0, 0, time.UTC)

func drain(t *testing.T, l Listening) []Event {
	t.Helper()
	var events []Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-l.Events():
			if !ok {
				return events
			}
			if e.Kind != Level {
				events = append(events, e)
			}
		case <-timeout:
			t.Fatal("events channel not closed")
		}
	}
}

func kinds(events []Event) []EventKind {
	var out []EventKind
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func TestSessionTranscribesEachEntry(t *testing.T) {
	src := newChanSource()
	tr := &fakeTranscriber{texts: []string{"income dua ratus ribu", "pengeluaran lima puluh ribu"}}
	s := Listen(context.Background(), src, tr, audio.DefaultSegmenterConfig(), func() time.Time { return start })
	src.feed(quiet(time.Second))
	src.feed(talk(time.Second)) // starts at 1 s
	src.feed(quiet(3 * time.Second))
	src.feed(talk(time.Second)) // starts at 5 s
	src.feed(quiet(3 * time.Second))
	time.Sleep(100 * time.Millisecond)
	s.Finish()
	events := drain(t, s)

	var heard []Event
	for _, e := range events {
		if e.Kind == Heard {
			heard = append(heard, e)
		}
	}
	if len(heard) != 2 || heard[0].Text != "income dua ratus ribu" || heard[1].Text != "pengeluaran lima puluh ribu" {
		t.Fatalf("heard = %+v\nall: %v", heard, kinds(events))
	}
	for i, want := range []time.Duration{time.Second, 5 * time.Second} {
		if d := heard[i].At.Sub(start) - want; d.Abs() > 50*time.Millisecond {
			t.Errorf("entry %d At = %v, want ~%v", i+1, heard[i].At.Sub(start), want)
		}
	}
	if events[0].Kind != Calibrating || events[1].Kind != Ready {
		t.Errorf("first events = %v, want Calibrating, Ready", kinds(events[:2]))
	}
	if tr.prompt != Prompt {
		t.Errorf("prompt = %q", tr.prompt)
	}
	select {
	case <-src.closed:
	default:
		t.Error("microphone not closed")
	}
}

func TestSessionFinishFlushesSpeechInProgress(t *testing.T) {
	src := newChanSource()
	tr := &fakeTranscriber{texts: []string{"income seribu"}}
	s := Listen(context.Background(), src, tr, audio.DefaultSegmenterConfig(), func() time.Time { return start })
	src.feed(quiet(time.Second))
	src.feed(talk(800 * time.Millisecond)) // user presses Enter while still talking
	time.Sleep(100 * time.Millisecond)
	s.Finish()
	events := drain(t, s)
	if n := countKind(events, Heard); n != 1 {
		t.Errorf("heard %d entries, want 1: %v", n, kinds(events))
	}
}

func TestSessionCloseAborts(t *testing.T) {
	src := newChanSource()
	tr := &fakeTranscriber{texts: []string{"x"}, block: make(chan struct{})}
	s := Listen(context.Background(), src, tr, audio.DefaultSegmenterConfig(), func() time.Time { return start })
	src.feed(quiet(time.Second))
	src.feed(talk(time.Second))
	src.feed(quiet(3 * time.Second)) // the entry ends and its transcription blocks
	time.Sleep(100 * time.Millisecond)
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked on a running transcription")
	}
	for e := range s.Events() {
		if e.Kind == Heard {
			t.Error("heard an entry after Close")
		}
	}
}

func TestSessionMicFailure(t *testing.T) {
	src := newChanSource()
	src.err = errors.New("device unplugged")
	s := Listen(context.Background(), src, &fakeTranscriber{texts: []string{"x"}}, audio.DefaultSegmenterConfig(), time.Now)
	close(src.frames)
	events := drain(t, s)
	last := events[len(events)-1]
	if last.Kind != Failed || !last.Fatal || last.Err.Error() != "device unplugged" {
		t.Errorf("last event = %+v", last)
	}
}

func countKind(events []Event, k EventKind) int {
	n := 0
	for _, e := range events {
		if e.Kind == k {
			n++
		}
	}
	return n
}
