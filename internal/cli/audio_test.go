package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"money-manager/internal/speech"
	"money-manager/internal/store"
)

type fakeAudio struct {
	report    speech.Report
	plan      speech.Plan
	installed []speech.Plan
	statusRun int
	install   func(ctx context.Context) error
}

func (f *fakeAudio) Status(_ context.Context, engineTest bool, note func(string)) speech.Report {
	f.statusRun++
	if engineTest && note != nil {
		note("Testing the speech engine...")
	}
	return f.report
}

func (f *fakeAudio) Plan(context.Context) (speech.Plan, error) { return f.plan, nil }

func (f *fakeAudio) Install(ctx context.Context, p speech.Plan, progress func(speech.Progress)) (speech.Paths, error) {
	f.installed = append(f.installed, p)
	if progress != nil {
		progress(speech.Progress{Label: "Downloading speech model", Done: 50 << 20, Total: 100 << 20, Rate: 10 << 20})
		progress(speech.Progress{Label: "Downloading speech model", Done: 100 << 20, Total: 100 << 20, Finished: true})
	}
	if f.install != nil {
		if err := f.install(ctx); err != nil {
			return speech.Paths{}, err
		}
	}
	return speech.Paths{Server: p.Dir + `\whisper-server.exe`, Model: p.Dir + `\model.bin`}, nil
}

var (
	readyReport = speech.Report{Checks: []speech.Check{
		{Name: "System", Detail: "Windows 64-bit"},
		{Name: "Speech program", Detail: `C:\w\whisper-server.exe`},
		{Name: "GPU", State: speech.Warn, Detail: "no NVIDIA GPU found", Fix: "The CPU will be used."},
		{Name: "Engine test", Detail: "model ready in 3.0 s on CPU"},
	}}
	missingReport = speech.Report{Checks: []speech.Check{
		{Name: "System", Detail: "Windows 64-bit"},
		{Name: "Speech program", State: speech.Fail, Detail: "not installed", Fix: "Run /setup-audio to download it."},
		{Name: "Speech model", State: speech.Fail, Detail: "not installed", Fix: "Run /setup-audio to download it."},
		{Name: "Engine test", State: speech.Skipped, Detail: "needs the speech program and model"},
	}}
	twoDownloads = []speech.Asset{
		{Label: "speech program (NVIDIA GPU version)", Size: 674_539_285, Zip: true},
		{Label: "speech model (Whisper large-v3-turbo)", Size: 574_041_195},
	}
)

func runAudio(t *testing.T, audio AudioTools, input string) string {
	t.Helper()
	var out bytes.Buffer
	r := &REPL{In: strings.NewReader(input), Out: &out, Store: store.NewMemory(),
		Now: func() time.Time { return fixedNow }, Audio: audio}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String()
}

func TestAudioStatusReady(t *testing.T) {
	out := runAudio(t, &fakeAudio{report: readyReport}, "/audio-status\n")
	assertContains(t, out,
		"Audio-to-text status",
		"Testing the speech engine...",
		`✓ System          Windows 64-bit`,
		`! GPU             no NVIDIA GPU found`,
		"→ The CPU will be used.",
		"Audio-to-text: READY",
		"Type /audio to record entries by voice.")
}

func TestAudioStatusNotReady(t *testing.T) {
	out := runAudio(t, &fakeAudio{report: missingReport}, "/audio-status\n")
	assertContains(t, out,
		"✗ Speech program  not installed",
		"– Engine test     needs the speech program and model",
		"Audio-to-text: NOT READY",
		"Next step: Run /setup-audio to download it.")
}

func TestSetupAudioAlreadyInstalled(t *testing.T) {
	f := &fakeAudio{plan: speech.Plan{Report: readyReport}}
	out := runAudio(t, f, "/setup-audio\n")
	assertContains(t, out, "Checking what is already installed...", "already installed. Nothing to download.", "/audio-status")
	if len(f.installed) != 0 {
		t.Error("Install called with nothing to do")
	}

	f = &fakeAudio{plan: speech.Plan{Report: readyReport, WriteEnv: true}}
	out = runAudio(t, f, "/setup-audio\n")
	assertContains(t, out, "Found an existing installation")
	if len(f.installed) != 1 {
		t.Error("existing install not saved to settings")
	}
}

func TestSetupAudioDownload(t *testing.T) {
	f := &fakeAudio{
		report: readyReport,
		plan:   speech.Plan{Report: missingReport, Dir: `C:\Users\x\whisper`, Downloads: twoDownloads},
	}
	out := runAudio(t, f, "/setup-audio\nrelative\\dir\n\"D:\\Tools\\whisper\"\n\n")
	assertContains(t, out,
		"✗ Speech program  not installed",
		"To download:",
		"• speech program (NVIDIA GPU version)     643 MB",
		"• speech model (Whisper large-v3-turbo)   547 MB",
		"Total download 1.2 GB · needs about 2.4 GB of disk space",
		`Install folder [C:\Users\x\whisper]: `,
		"✗ Use a full path",
		"Download 1.2 GB now? [Y/n]: ",
		"Press Ctrl+C to stop the download.",
		"Downloading speech model ██████████░░░░░░░░░░ 50 MB / 100 MB  50% 10 MB/s",
		"✓ speech model downloaded and verified (100 MB)",
		`✓ Installed in D:\Tools\whisper`,
		"Checking the new installation...",
		"Audio-to-text: READY")
	if len(f.installed) != 1 || f.installed[0].Dir != `D:\Tools\whisper` {
		t.Errorf("installed = %+v", f.installed)
	}
}

func TestSetupAudioDeclined(t *testing.T) {
	f := &fakeAudio{plan: speech.Plan{Report: missingReport, Dir: `C:\w`, Downloads: twoDownloads}}
	out := runAudio(t, f, "/setup-audio\n\nn\n")
	assertContains(t, out, "Cancelled. Nothing was downloaded.")
	if len(f.installed) != 0 {
		t.Error("Install called after the user said no")
	}
}

func TestSetupAudioInterrupt(t *testing.T) {
	started := make(chan struct{})
	f := &fakeAudio{plan: speech.Plan{Report: missingReport, Dir: `C:\w`, Downloads: twoDownloads}}
	f.install = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	var out bytes.Buffer
	r := &REPL{In: strings.NewReader("/setup-audio\n\ny\nincome 1\n"), Out: &out, Store: store.NewMemory(),
		Now: func() time.Time { return fixedNow }, Audio: f}
	if r.Interrupt() {
		t.Error("Interrupt() = true with nothing running")
	}
	go func() {
		<-started
		if !r.Interrupt() {
			t.Error("Interrupt() = false during download")
		}
	}()
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Download stopped. The unfinished file was deleted", "✓ Saved income 1.00")
	if r.Interrupt() {
		t.Error("Interrupt() = true after the download ended")
	}
}

func TestAudioCommandsUnavailable(t *testing.T) {
	out := runAudio(t, nil, "/audio-status\n/setup-audio\n")
	if strings.Count(out, "Voice input isn't available") != 2 {
		t.Errorf("output:\n%s", out)
	}
}

func TestAudioCommandsInTutorial(t *testing.T) {
	f := &fakeAudio{}
	out := runAudio(t, f, "/tutorial\n/setup-audio\nq\n")
	assertContains(t, out, "That works outside the tutorial.")
	if f.statusRun != 0 {
		t.Error("audio command ran inside the tutorial")
	}
}
