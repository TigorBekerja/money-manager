package speech

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"money-manager/internal/audio"
	"money-manager/internal/config"
)

// Paths locates the installed engine.
type Paths struct {
	Server string // whisper-server executable
	Model  string // ggml model file
}

type State int

const (
	OK State = iota
	Warn
	Fail
	Skipped
)

// Check is one line of the status report.
type Check struct {
	Name   string
	State  State
	Detail string
	Fix    string // what to do when State is Warn or Fail
}

type Report struct{ Checks []Check }

// Ready reports whether nothing failed.
func (r Report) Ready() bool {
	for _, c := range r.Checks {
		if c.State == Fail {
			return false
		}
	}
	return true
}

// Manager inspects, installs and tests the speech engine. The function fields are the
// system probes; NewManager sets the real ones and tests replace them.
type Manager struct {
	EnvPath    string // .env file holding WHISPER_SERVER / WHISPER_MODEL
	DefaultDir string // install folder suggested when nothing is installed

	Client    *http.Client
	DetectGPU func(context.Context) (GPU, error)
	Mics      func() ([]string, error)
	MicAccess func() (audio.Access, string)
	RunHelp   func(ctx context.Context, exe string) error
	Platform  string // GOOS/GOARCH
}

func NewManager(envPath, defaultDir string) *Manager {
	return &Manager{
		EnvPath:    envPath,
		DefaultDir: defaultDir,
		Client:     &http.Client{},
		DetectGPU:  detectGPU,
		Mics:       audio.InputDevices,
		MicAccess:  audio.MicrophoneAccess,
		RunHelp:    runHelp,
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// DefaultDir is %LOCALAPPDATA%\money-manager-vandika\whisper on Windows.
func DefaultDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "money-manager-vandika", "whisper"), nil
}

const (
	envServer = "WHISPER_SERVER"
	envModel  = "WHISPER_MODEL"
	envHeader = "Voice input (/audio), written by /setup-audio"
)

// minModelBytes rejects truncated or placeholder model files. Variable for tests.
var minModelBytes int64 = 50 << 20

func serverExeName() string {
	if runtime.GOOS == "windows" {
		return "whisper-server.exe"
	}
	return "whisper-server"
}

// paths returns the configured paths, falling back to the default install folder.
func (m *Manager) paths() (p Paths, configured bool) {
	vals, _ := config.ReadEnvFile(m.EnvPath)
	p = Paths{Server: vals[envServer], Model: vals[envModel]}
	configured = p.Server != "" && p.Model != ""
	if p.Server == "" {
		p.Server = filepath.Join(m.DefaultDir, serverExeName())
	}
	if p.Model == "" {
		p.Model = filepath.Join(m.DefaultDir, modelTurbo.Name)
	}
	return p, configured
}

// InstalledPaths returns the speech program and model, or an error telling the user to
// run /setup-audio.
func (m *Manager) InstalledPaths() (Paths, error) {
	p, _ := m.paths()
	for _, f := range []string{p.Server, p.Model} {
		if _, err := os.Stat(f); err != nil {
			return p, errors.New("Voice input isn't set up yet. Run /setup-audio first.")
		}
	}
	return p, nil
}

// Status checks everything voice input needs. With engineTest, it also starts the engine
// and transcribes a second of silence (takes a few seconds); note reports that progress.
func (m *Manager) Status(ctx context.Context, engineTest bool, note func(string)) Report {
	var r Report
	p, _ := m.paths()

	r.Checks = append(r.Checks, m.checkSystem())
	program := m.checkProgram(ctx, p)
	model := m.checkModel(p)
	gpu, gpuCheck := m.checkGPU(ctx)
	r.Checks = append(r.Checks, program, model, gpuCheck, m.checkMics(), m.checkMicAccess())

	if engineTest {
		if program.State == Fail || model.State == Fail {
			r.Checks = append(r.Checks, Check{Name: "Engine test", State: Skipped,
				Detail: "needs the speech program and model"})
		} else {
			r.Checks = append(r.Checks, m.engineTest(ctx, p, gpu, note))
		}
	}
	return r
}

func (m *Manager) checkSystem() Check {
	c := Check{Name: "System"}
	if m.Platform == "windows/amd64" {
		c.Detail = "Windows 64-bit"
		return c
	}
	c.State, c.Detail = Fail, m.Platform+" is not supported yet"
	c.Fix = "Voice input currently needs Windows 64-bit."
	return c
}

func (m *Manager) checkProgram(ctx context.Context, p Paths) Check {
	c := Check{Name: "Speech program"}
	if _, err := os.Stat(p.Server); err != nil {
		c.State, c.Detail, c.Fix = Fail, "not installed", "Run /setup-audio to download it."
		return c
	}
	if err := m.RunHelp(ctx, p.Server); err != nil {
		c.State, c.Detail = Fail, "found but can't start: "+err.Error()
		c.Fix = "Run /setup-audio to reinstall it."
		return c
	}
	c.Detail = p.Server
	if v, err := os.ReadFile(filepath.Join(filepath.Dir(p.Server), "version.txt")); err == nil {
		c.Detail += " (" + strings.TrimSpace(string(v)) + ")"
	}
	return c
}

func (m *Manager) checkModel(p Paths) Check {
	c := Check{Name: "Speech model"}
	fi, err := os.Stat(p.Model)
	switch {
	case err != nil:
		c.State, c.Detail, c.Fix = Fail, "not installed", "Run /setup-audio to download it."
	case fi.Size() < minModelBytes ||
		(filepath.Base(p.Model) == modelTurbo.Name && fi.Size() != modelTurbo.Size):
		c.State, c.Detail = Fail, fmt.Sprintf("%s looks incomplete (%s)", filepath.Base(p.Model), FormatBytes(fi.Size()))
		c.Fix = "Run /setup-audio to download it again."
	default:
		c.Detail = fmt.Sprintf("%s (%s)", filepath.Base(p.Model), FormatBytes(fi.Size()))
	}
	return c
}

func (m *Manager) checkGPU(ctx context.Context) (GPU, Check) {
	c := Check{Name: "GPU"}
	g, err := m.DetectGPU(ctx)
	switch {
	case err != nil:
		c.State, c.Detail = Warn, "couldn't check: "+err.Error()
		c.Fix = "The CPU will be used (slower, still works)."
	case g.Name == "":
		c.State, c.Detail = Warn, "no NVIDIA GPU found"
		c.Fix = "The CPU will be used (slower, still works)."
	case !g.Usable():
		c.State, c.Detail = Warn, fmt.Sprintf("%s, driver supports CUDA %.1f (needs %.1f)", g.Name, g.CUDA, minCUDA)
		c.Fix = "Update the NVIDIA driver for faster speech recognition; the CPU works meanwhile."
	default:
		c.Detail = fmt.Sprintf("%s (CUDA %.1f)", g.Name, g.CUDA)
	}
	return g, c
}

func (m *Manager) checkMics() Check {
	c := Check{Name: "Microphone"}
	names, err := m.Mics()
	switch {
	case err != nil:
		c.State, c.Detail = Warn, "couldn't check: "+err.Error()
	case len(names) == 0:
		c.State, c.Detail = Fail, "no microphone found"
		c.Fix = "Plug in a microphone or enable it in Settings → System → Sound."
	case len(names) == 1:
		c.Detail = names[0]
	default:
		c.Detail = fmt.Sprintf("%d input devices: %s", len(names), strings.Join(names, ", "))
	}
	return c
}

func (m *Manager) checkMicAccess() Check {
	c := Check{Name: "Microphone access"}
	access, detail := m.MicAccess()
	c.Detail = detail
	switch access {
	case audio.Denied:
		c.State = Fail
		c.Fix = `Settings → Privacy & security → Microphone: turn on "Microphone access" and "Let desktop apps access your microphone".`
	case audio.Unknown:
		c.State = Warn
	}
	return c
}

func (m *Manager) engineTest(ctx context.Context, p Paths, gpu GPU, note func(string)) Check {
	c := Check{Name: "Engine test"}
	if note != nil {
		note("Testing the speech engine (loading the model takes a few seconds)...")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	srv, err := StartServer(ctx, p, gpu.Usable())
	fellBack := false
	if err != nil && gpu.Usable() && ctx.Err() == nil {
		srv, err = StartServer(ctx, p, false)
		fellBack = err == nil
	}
	if err != nil {
		c.State, c.Detail, c.Fix = Fail, err.Error(), "Run /setup-audio to reinstall the speech program."
		return c
	}
	defer srv.Close()

	t0 := time.Now()
	if _, err := srv.Transcribe(ctx, audio.EncodeWAV(make([]int16, 16000), 16000), ""); err != nil {
		c.State, c.Detail, c.Fix = Fail, "test transcription failed: "+err.Error(), "Run /setup-audio to reinstall."
		return c
	}
	where := "CPU"
	if srv.GPU {
		where = "GPU"
	}
	c.Detail = fmt.Sprintf("model ready in %.1f s on %s, test transcription %.1f s",
		srv.Loaded.Seconds(), where, time.Since(t0).Seconds())
	if fellBack {
		c.State = Warn
		c.Fix = "The GPU version didn't start, so the CPU is used (slower). Updating the NVIDIA driver may help."
	}
	return c
}

// runHelp starts the program with --help to prove it and its DLLs load.
func runHelp(ctx context.Context, exe string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "--help").CombinedOutput()
	if err == nil || strings.Contains(strings.ToLower(string(out)), "usage") {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && uint32(exitErr.ExitCode()) == 0xC0000135 {
		return errors.New("a required DLL is missing (Microsoft Visual C++ Redistributable x64?)")
	}
	if errors.Is(err, fs.ErrPermission) {
		return errors.New("Windows blocked it from running")
	}
	return err
}

// FormatBytes renders a size as "547 MB" / "1.2 GB".
func FormatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", n>>10)
	}
}
