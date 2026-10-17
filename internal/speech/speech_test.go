package speech

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"money-manager/internal/audio"
	"money-manager/internal/config"
)

// The test binary doubles as a fake whisper-server when FAKE_WHISPER is set.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_WHISPER"); mode != "" {
		fakeWhisperServer(mode, os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

func fakeWhisperServer(mode string, args []string) {
	port, gpu := "", true
	for i, a := range args {
		switch a {
		case "--help":
			fmt.Println("usage: whisper-server [options]")
			return
		case "--port":
			port = args[i+1]
		case "-ng":
			gpu = false
		}
	}
	if mode == "crash" || (mode == "gpu-crash" && gpu) {
		fmt.Fprintln(os.Stderr, "error: failed to load model")
		os.Exit(1)
	}
	if gpu {
		fmt.Fprintln(os.Stderr, "whisper_backend_init_gpu: using CUDA0 backend")
	}
	start := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if time.Since(start) < 300*time.Millisecond {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"status":"loading model"}`)
			return
		}
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/inference", func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("file")
		if err != nil || r.FormValue("response_format") != "json" {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		f.Close()
		fmt.Fprintf(w, `{"text":" halo %s "}`, r.FormValue("prompt"))
	})
	http.ListenAndServe("127.0.0.1:"+port, mux)
}

func fakePaths(t *testing.T, mode string) Paths {
	t.Helper()
	t.Setenv("FAKE_WHISPER", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Paths{Server: exe, Model: "model.bin"}
}

func TestServerTranscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv, err := StartServer(ctx, fakePaths(t, "ok"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if !srv.GPU {
		t.Error("GPU = false, want true (log mentions CUDA0)")
	}
	if srv.Loaded < 300*time.Millisecond {
		t.Errorf("Loaded = %v, should include the loading phase", srv.Loaded)
	}
	text, err := srv.Transcribe(ctx, audio.EncodeWAV(make([]int16, 160), 16000), "income")
	if err != nil || text != "halo income" {
		t.Errorf("Transcribe = %q, %v", text, err)
	}
}

func TestServerCPUAndCrash(t *testing.T) {
	ctx := context.Background()
	srv, err := StartServer(ctx, fakePaths(t, "ok"), false)
	if err != nil {
		t.Fatal(err)
	}
	if srv.GPU {
		t.Error("GPU = true with -ng")
	}
	srv.Close()
	srv.Close() // idempotent

	_, err = StartServer(ctx, fakePaths(t, "crash"), true)
	if err == nil || !strings.Contains(err.Error(), "failed to load model") {
		t.Errorf("crash: err = %v, want the program's last log lines", err)
	}
}

func asset(name string, data []byte, url string, zip bool) Asset {
	sum := sha256.Sum256(data)
	return Asset{Name: name, Label: name, URL: url + "/" + name, Size: int64(len(data)),
		SHA256: hex.EncodeToString(sum[:]), Zip: zip}
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func fileServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m := NewManager(filepath.Join(dir, "config", ".env"), filepath.Join(dir, "whisper"))
	m.DetectGPU = func(context.Context) (GPU, error) { return GPU{Name: "RTX", CUDA: 13.1}, nil }
	m.Mics = func() ([]string, error) { return []string{"Microphone (Realtek)"}, nil }
	m.MicAccess = func() (audio.Access, string) { return audio.Allowed, "allowed for desktop apps" }
	m.RunHelp = func(context.Context, string) error { return nil }
	m.Platform = "windows/amd64"
	return m
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	m := testManager(t)
	good := []byte("model bytes")
	srv := fileServer(t, map[string][]byte{"m.bin": good, "bad.bin": []byte("tampered!!!")})
	dir := t.TempDir()

	var last Progress
	a := asset("m.bin", good, srv.URL, false)
	if err := m.download(context.Background(), a, filepath.Join(dir, "m.bin"), func(p Progress) { last = p }); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "m.bin")); string(got) != string(good) {
		t.Errorf("content = %q", got)
	}
	if !last.Finished || last.Done != int64(len(good)) {
		t.Errorf("last progress = %+v", last)
	}

	bad := a
	bad.URL = srv.URL + "/bad.bin"
	err := m.download(context.Background(), bad, filepath.Join(dir, "bad.bin"), func(Progress) {})
	if err == nil || !strings.Contains(err.Error(), "failed verification") {
		t.Errorf("tampered download: err = %v", err)
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "bad.bin*")); len(entries) != 0 {
		t.Errorf("left files behind: %v", entries)
	}

	missing := a
	missing.URL = srv.URL + "/nope"
	if err := m.download(context.Background(), missing, filepath.Join(dir, "x"), func(Progress) {}); err == nil ||
		!strings.Contains(err.Error(), "404") {
		t.Errorf("404: err = %v", err)
	}
}

func TestDownloadCancel(t *testing.T) {
	m := testManager(t)
	var served atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		served.Store(true)
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for !served.Load() {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	dir := t.TempDir()
	err := m.download(ctx, Asset{Name: "big", URL: srv.URL, Size: 1 << 30, SHA256: "x"}, filepath.Join(dir, "big"), func(Progress) {})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("partial file left behind: %v", entries)
	}
}

func TestExtractProgram(t *testing.T) {
	exe := serverExeName()
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
		want    []string
	}{
		{"release folder", map[string]string{"Release/" + exe: "exe", "Release/ggml.dll": "dll", "README.md": "x"}, "",
			[]string{exe, "ggml.dll"}},
		{"top level", map[string]string{exe: "exe", "cudart64_12.dll": "dll"}, "", []string{exe, "cudart64_12.dll"}},
		{"missing exe", map[string]string{"Release/other.exe": "x"}, "not found", nil},
		{"zip slip", map[string]string{"Release/" + exe: "exe", "Release/../../evil.dll": "x"}, "unsafe path", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			zipPath := filepath.Join(dir, "a.zip")
			os.WriteFile(zipPath, makeZip(t, tt.files), 0o644)
			out := filepath.Join(dir, "out")
			got, err := extractProgram(zipPath, out, exe)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %v, want %q", err, tt.wantErr)
				}
				if _, err := os.Stat(filepath.Join(dir, "evil.dll")); err == nil {
					t.Error("zip slip wrote outside the target")
				}
				return
			}
			if err != nil || got != filepath.Join(out, exe) {
				t.Fatalf("got %q, %v", got, err)
			}
			for _, f := range tt.want {
				if _, err := os.Stat(filepath.Join(out, f)); err != nil {
					t.Errorf("%s not extracted", f)
				}
			}
			if _, err := os.Stat(filepath.Join(out, "README.md")); err == nil {
				t.Error("file outside the program folder was extracted")
			}
		})
	}
}

func checkState(t *testing.T, r Report, name string, want State) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			if c.State != want {
				t.Errorf("%s: state %v (%s), want %v", name, c.State, c.Detail, want)
			}
			return c
		}
	}
	t.Fatalf("no %q check in %+v", name, r.Checks)
	return Check{}
}

func TestStatusNothingInstalled(t *testing.T) {
	m := testManager(t)
	r := m.Status(context.Background(), true, nil)
	if r.Ready() {
		t.Error("Ready() = true with nothing installed")
	}
	checkState(t, r, "System", OK)
	if c := checkState(t, r, "Speech program", Fail); !strings.Contains(c.Fix, "/setup-audio") {
		t.Errorf("fix = %q", c.Fix)
	}
	checkState(t, r, "Speech model", Fail)
	checkState(t, r, "GPU", OK)
	checkState(t, r, "Microphone", OK)
	checkState(t, r, "Microphone access", OK)
	checkState(t, r, "Engine test", Skipped)
}

func TestStatusProblems(t *testing.T) {
	m := testManager(t)
	m.Platform = "linux/amd64"
	m.DetectGPU = func(context.Context) (GPU, error) { return GPU{}, nil }
	m.Mics = func() ([]string, error) { return nil, nil }
	m.MicAccess = func() (audio.Access, string) { return audio.Denied, `"Microphone access" is turned off` }
	r := m.Status(context.Background(), false, nil)
	checkState(t, r, "System", Fail)
	checkState(t, r, "GPU", Warn)
	checkState(t, r, "Microphone", Fail)
	if c := checkState(t, r, "Microphone access", Fail); !strings.Contains(c.Fix, "Privacy & security") {
		t.Errorf("fix = %q", c.Fix)
	}
	for _, c := range r.Checks {
		if c.Name == "Engine test" {
			t.Error("engine test ran with engineTest=false")
		}
	}

	m.DetectGPU = func(context.Context) (GPU, error) { return GPU{Name: "GTX", CUDA: 11.4}, nil }
	if c := checkState(t, m.Status(context.Background(), false, nil), "GPU", Warn); !strings.Contains(c.Detail, "needs 12.4") {
		t.Errorf("old driver detail = %q", c.Detail)
	}
}

func installFake(t *testing.T, m *Manager, model []byte) Paths {
	t.Helper()
	exe, _ := os.Executable()
	modelPath := filepath.Join(t.TempDir(), modelTurbo.Name+".test")
	os.WriteFile(modelPath, model, 0o644)
	if err := config.SetValues(m.EnvPath, "", []config.KV{{Key: envServer, Value: exe}, {Key: envModel, Value: modelPath}}); err != nil {
		t.Fatal(err)
	}
	return Paths{Server: exe, Model: modelPath}
}

func TestStatusReadyWithEngineTest(t *testing.T) {
	old := minModelBytes
	minModelBytes = 4
	t.Cleanup(func() { minModelBytes = old })

	m := testManager(t)
	installFake(t, m, []byte("fake model"))
	t.Setenv("FAKE_WHISPER", "ok")
	var notes []string
	r := m.Status(context.Background(), true, func(s string) { notes = append(notes, s) })
	if !r.Ready() {
		t.Errorf("not ready: %+v", r.Checks)
	}
	if c := checkState(t, r, "Engine test", OK); !strings.Contains(c.Detail, "on GPU") {
		t.Errorf("engine detail = %q", c.Detail)
	}
	if len(notes) != 1 {
		t.Errorf("notes = %q", notes)
	}

	t.Setenv("FAKE_WHISPER", "gpu-crash")
	r = m.Status(context.Background(), true, nil)
	if c := checkState(t, r, "Engine test", Warn); !strings.Contains(c.Detail, "on CPU") {
		t.Errorf("fallback detail = %q", c.Detail)
	}
	if !r.Ready() {
		t.Error("CPU fallback should still be ready")
	}
}

func TestStatusIncompleteModel(t *testing.T) {
	m := testManager(t)
	p := installFake(t, m, []byte("tiny"))
	if c := checkState(t, m.Status(context.Background(), false, nil), "Speech model", Fail); !strings.Contains(c.Detail, "incomplete") {
		t.Errorf("detail = %q (%s)", c.Detail, p.Model)
	}
}

func TestPlanChoosesBuild(t *testing.T) {
	m := testManager(t)
	plan, err := m.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Downloads) != 2 || plan.Downloads[0].Name != programGPU.Name || plan.Downloads[1].Name != modelTurbo.Name {
		t.Errorf("GPU plan downloads = %+v", plan.Downloads)
	}
	if plan.Dir != m.DefaultDir || !plan.WriteEnv {
		t.Errorf("dir = %q writeEnv = %v", plan.Dir, plan.WriteEnv)
	}

	m.DetectGPU = func(context.Context) (GPU, error) { return GPU{}, nil }
	plan, _ = m.Plan(context.Background())
	if plan.Downloads[0].Name != programCPU.Name {
		t.Errorf("no-GPU plan uses %s", plan.Downloads[0].Name)
	}

	m.Platform = "darwin/arm64"
	if _, err := m.Plan(context.Background()); err == nil {
		t.Error("unsupported platform: want error")
	}
}

func TestPlanNothingToDo(t *testing.T) {
	old := minModelBytes
	minModelBytes = 4
	t.Cleanup(func() { minModelBytes = old })
	m := testManager(t)
	p := installFake(t, m, []byte("fake model"))
	plan, err := m.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Downloads) != 0 || plan.WriteEnv {
		t.Errorf("installed: downloads=%v writeEnv=%v", plan.Downloads, plan.WriteEnv)
	}
	if plan.Dir != filepath.Dir(p.Server) {
		t.Errorf("dir = %q, want the program's folder", plan.Dir)
	}
}

func TestInstall(t *testing.T) {
	m := testManager(t)
	exe := serverExeName()
	zipData := makeZip(t, map[string]string{"Release/" + exe: "exe", "Release/whisper.dll": "dll"})
	model := []byte("the model")
	srv := fileServer(t, map[string][]byte{"prog.zip": zipData, modelTurbo.Name: model})

	config.SetValues(m.EnvPath, "", []config.KV{{Key: "DB_PASSWORD", Value: "secret"}})
	plan, _ := m.Plan(context.Background())
	prog := asset("prog.zip", zipData, srv.URL, true)
	prog.Label = "speech program (CPU version)"
	plan.Downloads = []Asset{prog, asset(modelTurbo.Name, model, srv.URL, false)}

	var labels []string
	paths, err := m.Install(context.Background(), plan, func(p Progress) {
		if p.Finished {
			labels = append(labels, p.Label)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if paths.Server != filepath.Join(plan.Dir, exe) || paths.Model != filepath.Join(plan.Dir, modelTurbo.Name) {
		t.Errorf("paths = %+v", paths)
	}
	for _, f := range []string{exe, "whisper.dll", modelTurbo.Name, "version.txt"} {
		if _, err := os.Stat(filepath.Join(plan.Dir, f)); err != nil {
			t.Errorf("%s missing", f)
		}
	}
	if _, err := os.Stat(filepath.Join(plan.Dir, ".download")); err == nil {
		t.Error(".download folder not cleaned up")
	}
	if v, _ := os.ReadFile(filepath.Join(plan.Dir, "version.txt")); !strings.Contains(string(v), "v1.9.4") ||
		!strings.Contains(string(v), "CPU version") {
		t.Errorf("version.txt = %q", v)
	}
	vals, _ := config.ReadEnvFile(m.EnvPath)
	if vals[envServer] != paths.Server || vals[envModel] != paths.Model || vals["DB_PASSWORD"] != "secret" {
		t.Errorf(".env = %v", vals)
	}
	want := "Downloading speech program (CPU version)|Unpacking speech program (CPU version)|Downloading " + modelTurbo.Name
	if strings.Join(labels, "|") != want {
		t.Errorf("progress labels = %q", labels)
	}
}

func TestInstallNotEnoughSpace(t *testing.T) {
	m := testManager(t)
	plan, _ := m.Plan(context.Background())
	plan.Downloads = []Asset{{Name: "huge", Size: 1 << 62}}
	if free := plan.FreeBytes(); free < 0 {
		t.Skip("free space unknown on this platform")
	}
	if _, err := m.Install(context.Background(), plan, nil); err == nil || !strings.Contains(err.Error(), "disk space") {
		t.Errorf("err = %v", err)
	}
}

func TestParseCUDAVersion(t *testing.T) {
	banner := "| NVIDIA-SMI 592.27   Driver Version: 592.27   CUDA Version: 13.1     |"
	if v := parseCUDAVersion(banner); v != 13.1 {
		t.Errorf("got %v", v)
	}
	if v := parseCUDAVersion("no gpu"); v != 0 {
		t.Errorf("got %v", v)
	}
	if !(GPU{Name: "x", CUDA: 12.4}).Usable() || (GPU{Name: "x", CUDA: 12.2}).Usable() || (GPU{CUDA: 13}).Usable() {
		t.Error("Usable thresholds wrong")
	}
}
