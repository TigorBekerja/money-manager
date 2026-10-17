package audio

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 17, 14, 32, 0, 0, time.UTC)

func silence(d time.Duration) []int16 { return make([]int16, int(d.Seconds()*SampleRate)) }

func noise(d time.Duration, amp float64) []int16 {
	s := make([]int16, int(d.Seconds()*SampleRate))
	for i := range s {
		s[i] = int16(amp * math.Sin(float64(i)*1.7) * math.Cos(float64(i)*0.31))
	}
	return s
}

func tone(d time.Duration, amp float64) []int16 {
	s := make([]int16, int(d.Seconds()*SampleRate))
	for i := range s {
		s[i] = int16(amp * math.Sin(2*math.Pi*220*float64(i)/SampleRate))
	}
	return s
}

func concat(parts ...[]int16) []int16 {
	var out []int16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func collect(events []Event, kind EventKind) []Event {
	var out []Event
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// push feeds audio in 100 ms chunks like a microphone.
func push(s *Segmenter, samples []int16) []Event {
	var events []Event
	for i := 0; i < len(samples); i += 1600 {
		events = append(events, s.Push(samples[i:min(i+1600, len(samples))])...)
	}
	return events
}

func TestSegmenterTwoUtterances(t *testing.T) {
	s := NewSegmenter(DefaultSegmenterConfig(), t0)
	stream := concat(
		noise(time.Second, 60),            // background, includes calibration
		tone(1500*time.Millisecond, 8000), // entry 1 starts at 1.0 s
		noise(time.Second, 60),            // pause > 0.8 s
		tone(time.Second, 8000),           // entry 2 starts at 3.5 s
		noise(1200*time.Millisecond, 60),
	)
	events := push(s, stream)

	if len(collect(events, Calibrated)) != 1 {
		t.Error("expected one Calibrated event")
	}
	if len(collect(events, Level)) == 0 {
		t.Error("no level events")
	}
	starts := collect(events, SpeechStarted)
	utts := collect(events, Utterance)
	if len(starts) != 2 || len(utts) != 2 {
		t.Fatalf("starts=%d utterances=%d, want 2 each", len(starts), len(utts))
	}
	for i, want := range []time.Duration{time.Second, 3500 * time.Millisecond} {
		if d := utts[i].At.Sub(t0) - want; d < -40*time.Millisecond || d > 40*time.Millisecond {
			t.Errorf("utterance %d starts at %v, want ~%v", i+1, utts[i].At.Sub(t0), want)
		}
		if !starts[i].At.Equal(utts[i].At) {
			t.Errorf("SpeechStarted and Utterance times differ for %d", i+1)
		}
	}
	// Includes the pre-roll and at most ~200 ms of trailing silence.
	if got := time.Duration(len(utts[0].Samples)) * time.Second / SampleRate; got < 1500*time.Millisecond || got > 2200*time.Millisecond {
		t.Errorf("utterance 1 length %v", got)
	}
}

func TestSegmenterIgnoresClicksAndNoise(t *testing.T) {
	s := NewSegmenter(DefaultSegmenterConfig(), t0)
	events := push(s, concat(
		noise(time.Second, 200),
		tone(100*time.Millisecond, 9000), // click: shorter than MinSpeech
		noise(2*time.Second, 200),
		noise(2*time.Second, 400), // background gets a bit louder: still not speech
	))
	if n := len(collect(events, Utterance)); n != 0 {
		t.Errorf("%d utterances from noise", n)
	}
	if n := len(collect(events, Dropped)); n != 1 {
		t.Errorf("%d dropped, want 1 (the click)", n)
	}
}

func TestSegmenterMaxLengthAndFlush(t *testing.T) {
	cfg := DefaultSegmenterConfig()
	cfg.MaxLength = 2 * time.Second
	s := NewSegmenter(cfg, t0)
	events := push(s, concat(silence(time.Second), tone(3*time.Second, 8000)))
	if n := len(collect(events, Utterance)); n != 1 {
		t.Fatalf("max length: %d utterances, want 1", n)
	}
	// Speech still going: Flush ends it.
	flushed := s.Flush()
	if len(collect(flushed, Utterance)) != 1 {
		t.Errorf("Flush = %+v, want the running utterance", flushed)
	}
	if s.Flush() != nil {
		t.Error("second Flush returned events")
	}
}

func TestWAVRoundTripAndFileSource(t *testing.T) {
	samples := concat(silence(300*time.Millisecond), tone(200*time.Millisecond, 1000))
	got, err := DecodeWAV(EncodeWAV(samples, SampleRate))
	if err != nil || len(got) != len(samples) || got[5000] != samples[5000] {
		t.Fatalf("round trip: len %d, err %v", len(got), err)
	}
	if _, err := DecodeWAV(EncodeWAV(samples, 44100)); err == nil {
		t.Error("44.1 kHz accepted")
	}

	path := filepath.Join(t.TempDir(), "a.wav")
	os.WriteFile(path, EncodeWAV(samples, SampleRate), 0o644)
	src, err := OpenWAVFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for c := range src.Frames() {
		n += len(c)
		if n > len(samples)+SampleRate { // file, then silence keeps coming
			break
		}
	}
	src.Close()
	if n <= len(samples) {
		t.Errorf("got %d samples, want the file followed by silence", n)
	}
}
