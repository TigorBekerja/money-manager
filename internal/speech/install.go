package speech

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"money-manager/internal/config"
)

// Plan is what /setup-audio would do, based on a quick check of the current setup.
type Plan struct {
	Report    Report  // quick checks (no engine test)
	Paths     Paths   // current paths; kept for parts that are already fine
	Dir       string  // install folder for downloads (the user may change it)
	Downloads []Asset // empty when everything is installed
	GPU       GPU
	WriteEnv  bool // .env lacks the paths even though the files exist
}

// NeedBytes estimates disk space: downloads plus the unpacked program.
func (p Plan) NeedBytes() int64 {
	var n int64
	for _, a := range p.Downloads {
		n += a.Size
		if a.Zip {
			n += 2 * a.Size
		}
	}
	return n
}

// DownloadBytes is the total download size.
func (p Plan) DownloadBytes() int64 {
	var n int64
	for _, a := range p.Downloads {
		n += a.Size
	}
	return n
}

// FreeBytes returns free space where the install folder lives, or -1 if unknown.
func (p Plan) FreeBytes() int64 { return freeSpace(p.Dir) }

// Plan checks the current setup and decides what to download.
func (m *Manager) Plan(ctx context.Context) (Plan, error) {
	paths, configured := m.paths()
	report := m.Status(ctx, false, nil)
	plan := Plan{Report: report, Paths: paths, Dir: m.DefaultDir}

	if c := report.Checks[0]; c.State == Fail {
		return plan, errors.New(c.Detail + ". " + c.Fix)
	}
	program, model := report.Checks[1], report.Checks[2]
	plan.GPU, _ = m.DetectGPU(ctx)

	switch {
	case program.State != Fail:
		plan.Dir = filepath.Dir(paths.Server)
	case model.State != Fail:
		plan.Dir = filepath.Dir(paths.Model)
	}
	if program.State == Fail {
		if plan.GPU.Usable() {
			plan.Downloads = append(plan.Downloads, programGPU)
		} else {
			plan.Downloads = append(plan.Downloads, programCPU)
		}
	}
	if model.State == Fail {
		plan.Downloads = append(plan.Downloads, modelTurbo)
	}
	plan.WriteEnv = len(plan.Downloads) > 0 || !configured
	return plan, nil
}

// Progress reports download progress.
type Progress struct {
	Label    string
	Done     int64
	Total    int64
	Rate     float64 // bytes per second
	Finished bool
}

// Install downloads and verifies plan.Downloads into plan.Dir, unpacks the program, and
// records the paths in the .env file. If ctx is cancelled, partial files are removed.
func (m *Manager) Install(ctx context.Context, plan Plan, progress func(Progress)) (Paths, error) {
	if progress == nil {
		progress = func(Progress) {}
	}
	paths := plan.Paths
	if len(plan.Downloads) > 0 {
		if free := freeSpace(plan.Dir); free >= 0 && free < plan.NeedBytes() {
			return paths, fmt.Errorf("not enough disk space in %s: need about %s, %s free",
				plan.Dir, FormatBytes(plan.NeedBytes()), FormatBytes(free))
		}
		if err := os.MkdirAll(plan.Dir, 0o755); err != nil {
			return paths, err
		}
	}

	for _, a := range plan.Downloads {
		if !a.Zip {
			dest := filepath.Join(plan.Dir, a.Name)
			if err := m.download(ctx, a, dest, progress); err != nil {
				return paths, err
			}
			paths.Model = dest
			continue
		}

		tmpDir := filepath.Join(plan.Dir, ".download")
		zipPath := filepath.Join(tmpDir, a.Name)
		err := m.download(ctx, a, zipPath, progress)
		if err == nil {
			progress(Progress{Label: "Unpacking " + a.Label})
			paths.Server, err = extractProgram(zipPath, plan.Dir, serverExeName())
			progress(Progress{Label: "Unpacking " + a.Label, Finished: true})
		}
		os.RemoveAll(tmpDir)
		if err != nil {
			return paths, err
		}
		version := buildName + ", " + strings.TrimPrefix(strings.TrimSuffix(a.Label, ")"), "speech program (")
		os.WriteFile(filepath.Join(plan.Dir, "version.txt"), []byte(version+"\n"), 0o644)
	}

	if plan.WriteEnv {
		err := config.SetValues(m.EnvPath, envHeader, []config.KV{
			{Key: envServer, Value: paths.Server},
			{Key: envModel, Value: paths.Model},
		})
		if err != nil {
			return paths, fmt.Errorf("save paths to %s: %w", m.EnvPath, err)
		}
	}
	return paths, nil
}

func (m *Manager) download(ctx context.Context, a Asset, dest string, progress func(Progress)) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	label := "Downloading " + a.Label
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "money-manager-vandika")
	resp, err := m.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("download %s: %w", a.Label, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: server returned %s", a.Label, resp.Status)
	}

	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	pw := &progressWriter{label: label, total: a.Size, report: progress, start: time.Now()}
	_, err = io.Copy(io.MultiWriter(f, h, pw), resp.Body)
	f.Close()
	if err != nil {
		os.Remove(part)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("download %s: %w", a.Label, err)
	}
	if pw.done != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		os.Remove(part)
		return fmt.Errorf("%s failed verification (size or checksum mismatch), so it was deleted. "+
			"The download was corrupted or the file changed on the server", a.Label)
	}
	pw.finish()
	return os.Rename(part, dest)
}

type progressWriter struct {
	label  string
	total  int64
	done   int64
	start  time.Time
	last   time.Time
	report func(Progress)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.done += int64(len(p))
	if now := time.Now(); now.Sub(w.last) >= 150*time.Millisecond {
		w.last = now
		w.report(w.snapshot(false))
	}
	return len(p), nil
}

func (w *progressWriter) finish() { w.report(w.snapshot(true)) }

func (w *progressWriter) snapshot(finished bool) Progress {
	rate := 0.0
	if s := time.Since(w.start).Seconds(); s > 0 {
		rate = float64(w.done) / s
	}
	return Progress{Label: w.label, Done: w.done, Total: w.total, Rate: rate, Finished: finished}
}

// extractProgram unpacks the folder of the archive that contains exeName into dir and
// returns the path of the extracted executable.
func extractProgram(zipPath, dir, exeName string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer zr.Close()

	prefix := ""
	found := false
	for _, f := range zr.File {
		if path.Base(f.Name) == exeName {
			prefix, found = path.Dir(f.Name), true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("%s not found in the downloaded archive", exeName)
	}

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel := f.Name
		if prefix != "." {
			if !strings.HasPrefix(f.Name, prefix+"/") {
				continue
			}
			rel = strings.TrimPrefix(f.Name, prefix+"/")
		}
		if !filepath.IsLocal(filepath.FromSlash(rel)) {
			return "", fmt.Errorf("unsafe path %q in the downloaded archive", f.Name)
		}
		if err := extractFile(f, filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, exeName), nil
}

func extractFile(f *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
