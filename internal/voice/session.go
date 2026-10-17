package voice

import (
	"context"
	"errors"
	"sync"
	"time"

	"money-manager/internal/audio"
	"money-manager/internal/speech"
)

// Prompt biases Whisper toward the app's words and number style.
const Prompt = "income dua ratus ribu. pengeluaran lima puluh ribu. pendapatan satu juta."

type Transcriber interface {
	Transcribe(ctx context.Context, wav []byte, prompt string) (string, error)
}

type EventKind int

const (
	Calibrating   EventKind = iota // measuring background noise; ask for quiet
	Ready                          // waiting for the next entry
	Level                          // microphone level, Level in 0..1
	Recording                      // speech detected, At = start
	Understanding                  // an entry is being transcribed, At = its start
	Heard                          // Text = transcript of the entry that started at At
	Failed                         // Err; Fatal means listening stopped
)

type Event struct {
	Kind  EventKind
	Level float64
	At    time.Time
	Text  string
	Err   error
	Fatal bool
}

// Listening is a running listening session.
type Listening interface {
	Events() <-chan Event
	Finish() // stop the microphone, finish entries already spoken, then close Events
	Close()  // stop immediately, dropping anything not yet transcribed
}

// Engine is a started speech engine that can listen several times.
type Engine interface {
	Listen(ctx context.Context) (Listening, error)
	GPU() bool
	Close() error
}

// Session implements Listening.
type Session struct {
	src    audio.Source
	tr     Transcriber
	cfg    audio.SegmenterConfig
	now    func() time.Time
	events chan Event
	finish chan struct{}
	once   sync.Once
	cancel context.CancelFunc
	done   chan struct{}
}

// Listen starts segmenting src and transcribing each utterance with tr.
func Listen(ctx context.Context, src audio.Source, tr Transcriber, cfg audio.SegmenterConfig, now func() time.Time) *Session {
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{src: src, tr: tr, cfg: cfg, now: now, events: make(chan Event, 64),
		finish: make(chan struct{}), cancel: cancel, done: make(chan struct{})}
	go s.run(ctx)
	return s
}

func (s *Session) Events() <-chan Event { return s.events }
func (s *Session) Finish()              { s.once.Do(func() { close(s.finish) }) }
func (s *Session) Close() {
	s.cancel()
	<-s.done
}

func (s *Session) emit(ctx context.Context, e Event) {
	select {
	case s.events <- e:
	case <-ctx.Done():
	}
}

func (s *Session) run(ctx context.Context) {
	defer close(s.done)
	defer close(s.events)

	queue := make(chan audio.Event, 32)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for u := range queue {
			s.emit(ctx, Event{Kind: Understanding, At: u.At})
			text, err := s.tr.Transcribe(ctx, audio.EncodeWAV(u.Samples, audio.SampleRate), Prompt)
			if ctx.Err() != nil {
				continue // aborted: drain the queue without transcribing
			}
			if err != nil {
				s.emit(ctx, Event{Kind: Failed, At: u.At, Err: err})
			} else {
				s.emit(ctx, Event{Kind: Heard, At: u.At, Text: text})
			}
		}
	}()

	seg := audio.NewSegmenter(s.cfg, s.now())
	handle := func(evs []audio.Event) {
		for _, e := range evs {
			switch e.Kind {
			case audio.Calibrated:
				s.emit(ctx, Event{Kind: Ready})
			case audio.Level:
				s.emit(ctx, Event{Kind: Level, Level: e.Level})
			case audio.SpeechStarted:
				s.emit(ctx, Event{Kind: Recording, At: e.At})
			case audio.Utterance:
				queue <- e
				s.emit(ctx, Event{Kind: Ready})
			case audio.Dropped:
				s.emit(ctx, Event{Kind: Ready})
			}
		}
	}

	s.emit(ctx, Event{Kind: Calibrating})
	frames := s.src.Frames()
loop:
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				err := s.src.Err()
				if err == nil {
					err = errors.New("the microphone stopped")
				}
				s.emit(ctx, Event{Kind: Failed, Err: err, Fatal: true})
				break loop
			}
			handle(seg.Push(f))
		case <-s.finish:
			handle(seg.Flush())
			break loop
		case <-ctx.Done():
			break loop
		}
	}
	s.src.Close()
	close(queue)
	wg.Wait()
}

// Launcher starts an Engine from the installed speech program (cli.VoiceInput).
type Launcher struct {
	Paths func() (speech.Paths, error) // e.g. (*speech.Manager).InstalledPaths
	Open  func() (audio.Source, error) // e.g. audio.OpenMic
}

func (l Launcher) Start(ctx context.Context) (Engine, error) {
	p, err := l.Paths()
	if err != nil {
		return nil, err
	}
	return StartEngine(ctx, p, l.Open)
}

// engine implements Engine with a whisper-server and a microphone opener.
type engine struct {
	srv  *speech.Server
	open func() (audio.Source, error)
}

// StartEngine starts whisper-server (GPU first, CPU if that fails). open is called for
// every Listen to get a fresh microphone.
func StartEngine(ctx context.Context, p speech.Paths, open func() (audio.Source, error)) (Engine, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	srv, err := speech.StartServer(ctx, p, true)
	if err != nil && ctx.Err() == nil {
		srv, err = speech.StartServer(ctx, p, false)
	}
	if err != nil {
		return nil, err
	}
	return &engine{srv: srv, open: open}, nil
}

func (e *engine) GPU() bool    { return e.srv.GPU }
func (e *engine) Close() error { return e.srv.Close() }

func (e *engine) Listen(ctx context.Context) (Listening, error) {
	src, err := e.open()
	if err != nil {
		return nil, err
	}
	return Listen(ctx, src, e.srv, audio.DefaultSegmenterConfig(), time.Now), nil
}
