package audio

import (
	"os"
	"testing"
	"time"
)

// TestMicCapture records one second from the real default microphone. Opt-in:
// MM_TEST_MIC=1 go test -run TestMicCapture -v ./internal/audio/
// Only the sample count and loudness are printed; nothing is saved.
func TestMicCapture(t *testing.T) {
	if os.Getenv("MM_TEST_MIC") != "1" {
		t.Skip("set MM_TEST_MIC=1 to record from the real microphone")
	}
	src, err := OpenMic()
	if err != nil {
		t.Fatal(err)
	}
	var samples []int16
	deadline := time.After(time.Second)
loop:
	for {
		select {
		case c, ok := <-src.Frames():
			if !ok {
				t.Fatalf("frames closed early: %v", src.Err())
			}
			samples = append(samples, c...)
		case <-deadline:
			break loop
		}
	}
	src.Close()
	src.Close() // idempotent
	if len(samples) < SampleRate*8/10 {
		t.Errorf("got %d samples in 1 s, want ~%d", len(samples), SampleRate)
	}
	t.Logf("captured %d samples, RMS %.0f (meter %.2f)", len(samples), rms(samples), meter(rms(samples)))
}
