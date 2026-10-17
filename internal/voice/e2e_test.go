package voice

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"money-manager/internal/audio"
	"money-manager/internal/speech"
)

// TestEndToEndWAV runs a recorded WAV through the real pipeline: segmenter, the installed
// whisper-server, and Interpret. Opt-in:
//
//	MM_E2E_WAV=entries.wav MM_E2E_EXPECT="income 200000;expense 55000" \
//	WHISPER_SERVER=...\whisper-server.exe WHISPER_MODEL=...\model.bin go test -run EndToEnd -v ./internal/voice/
func TestEndToEndWAV(t *testing.T) {
	wav, expect := os.Getenv("MM_E2E_WAV"), os.Getenv("MM_E2E_EXPECT")
	paths := speech.Paths{Server: os.Getenv("WHISPER_SERVER"), Model: os.Getenv("WHISPER_MODEL")}
	if wav == "" || paths.Server == "" || paths.Model == "" {
		t.Skip("set MM_E2E_WAV, MM_E2E_EXPECT, WHISPER_SERVER and WHISPER_MODEL")
	}
	var want []string
	for _, w := range strings.Split(expect, ";") {
		if w = strings.TrimSpace(w); w != "" {
			want = append(want, w)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	eng, err := StartEngine(ctx, paths, func() (audio.Source, error) { return audio.OpenWAVFile(wav, false) })
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	t.Logf("engine started, GPU=%v", eng.GPU())

	l, err := eng.Listen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	start := time.Now()
	for ev := range l.Events() {
		switch ev.Kind {
		case Heard:
			d, err := Interpret(ev.Text, ev.At)
			if IsStop(ev.Text) {
				t.Logf("heard %q → stop", ev.Text)
				got = append(got, "stop")
			} else if err != nil {
				t.Logf("heard %q → %v", ev.Text, err)
				got = append(got, "error")
			} else {
				t.Logf("heard %q → %s %d (starts %.1fs into the file)", ev.Text, d.Kind, d.Amount/100, ev.At.Sub(start).Seconds())
				got = append(got, string(d.Kind)+" "+strconv.FormatInt(d.Amount/100, 10))
			}
			if len(got) == len(want) {
				l.Finish()
			}
		case Failed:
			t.Errorf("failed: %v", ev.Err)
		}
	}
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
