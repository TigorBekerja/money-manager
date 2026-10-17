package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is a running whisper-server process listening on 127.0.0.1.
type Server struct {
	cmd    *exec.Cmd
	base   string
	logs   *syncBuffer
	done   chan struct{}
	GPU    bool // the engine reported a CUDA backend
	unjob  func()
	Loaded time.Duration // time until the model was ready
	client *http.Client
}

// StartServer launches whisper-server with the model and waits until it is ready or ctx
// ends. With useGPU false the server runs CPU-only.
func StartServer(ctx context.Context, p Paths, useGPU bool) (*Server, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	args := []string{"-m", p.Model, "-l", "id", "--host", "127.0.0.1", "--port", strconv.Itoa(port)}
	if !useGPU {
		args = append(args, "-ng")
	}
	logs := &syncBuffer{}
	cmd := exec.Command(p.Server, args...)
	cmd.Dir = filepath.Dir(p.Server)
	cmd.Stdout, cmd.Stderr = logs, logs

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", filepath.Base(p.Server), err)
	}
	s := &Server{
		cmd:    cmd,
		base:   "http://127.0.0.1:" + strconv.Itoa(port),
		logs:   logs,
		done:   make(chan struct{}),
		client: &http.Client{Timeout: 2 * time.Minute},
	}
	s.unjob = killWithParent(cmd.Process)
	go func() { cmd.Wait(); close(s.done) }()

	if err := s.waitReady(ctx); err != nil {
		s.Close()
		return nil, err
	}
	s.Loaded = time.Since(start)
	log := logs.String()
	s.GPU = useGPU && (strings.Contains(log, "using CUDA") || strings.Contains(log, "CUDA0"))
	return s, nil
}

func (s *Server) waitReady(ctx context.Context) error {
	probe := &http.Client{Timeout: 2 * time.Second}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-s.done:
			return fmt.Errorf("speech program stopped while loading:%s", s.lastLog(6))
		case <-ctx.Done():
			return fmt.Errorf("speech program not ready in time: %w", ctx.Err())
		case <-tick.C:
		}
		resp, err := probe.Get(s.base + "/health")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
	}
}

// Transcribe sends a WAV file and returns the recognised text (trimmed). prompt biases
// the vocabulary and may be empty.
func (s *Server) Transcribe(ctx context.Context, wav []byte, prompt string) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", err
	}
	fw.Write(wav)
	mw.WriteField("response_format", "json")
	mw.WriteField("temperature", "0.0")
	if prompt != "" {
		mw.WriteField("prompt", prompt)
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("speech program returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("unexpected reply from speech program: %w", err)
	}
	if out.Error != "" {
		return "", errors.New(out.Error)
	}
	return strings.TrimSpace(out.Text), nil
}

// Close stops the process.
func (s *Server) Close() error {
	select {
	case <-s.done:
	default:
		s.cmd.Process.Kill()
		<-s.done
	}
	if s.unjob != nil {
		s.unjob()
		s.unjob = nil
	}
	return nil
}

func (s *Server) lastLog(n int) string {
	lines := strings.Split(strings.TrimSpace(s.logs.String()), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return " (no output)"
	}
	return "\n      " + strings.Join(lines, "\n      ")
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
