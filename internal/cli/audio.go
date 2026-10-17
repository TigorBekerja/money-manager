package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"money-manager/internal/speech"
)

// AudioTools is implemented by *speech.Manager.
type AudioTools interface {
	Status(ctx context.Context, engineTest bool, note func(string)) speech.Report
	Plan(ctx context.Context) (speech.Plan, error)
	Install(ctx context.Context, plan speech.Plan, progress func(speech.Progress)) (speech.Paths, error)
}

// ops tracks the long-running operation (a download) that Ctrl+C should cancel.
type ops struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (o *ops) start(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	o.mu.Lock()
	o.cancel = cancel
	o.mu.Unlock()
	return ctx, func() {
		o.mu.Lock()
		o.cancel = nil
		o.mu.Unlock()
		cancel()
	}
}

func (o *ops) interrupt() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cancel == nil {
		return false
	}
	o.cancel()
	return true
}

var errNoAudio = errors.New("Voice input isn't available in this build.")

func (s *session) audioStatus(ctx context.Context) error {
	if s.audio == nil {
		renderError(s.out, s.st, errNoAudio)
		return nil
	}
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+s.st.bold("Audio-to-text status"))
	fmt.Fprintln(s.out, indent+s.st.dim("Checking..."))
	r := s.audio.Status(ctx, true, func(note string) { fmt.Fprintln(s.out, indent+s.st.dim(note)) })
	s.renderReport(r)
	s.renderVerdict(r)
	return nil
}

func (s *session) setupAudio(ctx context.Context) error {
	if s.audio == nil {
		renderError(s.out, s.st, errNoAudio)
		return nil
	}
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+s.st.bold("Set up audio-to-text"))
	fmt.Fprintln(s.out, indent+s.st.dim("Checking what is already installed..."))
	plan, err := s.audio.Plan(ctx)
	if err != nil {
		renderError(s.out, s.st, err)
		return nil
	}
	s.renderReport(plan.Report)
	fmt.Fprintln(s.out)

	if len(plan.Downloads) == 0 {
		if plan.WriteEnv {
			if _, err := s.audio.Install(ctx, plan, nil); err != nil {
				renderError(s.out, s.st, err)
				return nil
			}
			fmt.Fprintln(s.out, indent+s.st.green("✓")+" Found an existing installation and saved its location in your settings.")
		} else {
			fmt.Fprintln(s.out, indent+s.st.green("✓")+" The speech program and model are already installed. Nothing to download.")
		}
		fmt.Fprintln(s.out, indent+"Run /audio-status for a full check including a test of the speech engine.")
		return nil
	}

	fmt.Fprintln(s.out, indent+"To download:")
	labelW := 0
	for _, a := range plan.Downloads {
		labelW = max(labelW, len(a.Label))
	}
	for _, a := range plan.Downloads {
		fmt.Fprintf(s.out, "%s  • %s  %s\n", indent, padRight(a.Label, labelW), padLeft(speech.FormatBytes(a.Size), 7))
	}
	fmt.Fprintf(s.out, "%sTotal download %s · needs about %s of disk space\n", indent,
		speech.FormatBytes(plan.DownloadBytes()), speech.FormatBytes(plan.NeedBytes()))
	fmt.Fprintln(s.out)

	dir, err := s.askFolder(plan.Dir)
	if err != nil || dir == "" {
		return err
	}
	plan.Dir = dir
	if free := plan.FreeBytes(); free >= 0 && free < plan.NeedBytes() {
		renderError(s.out, s.st, fmt.Errorf("Not enough disk space in %s: need about %s, %s free. Choose another folder.",
			dir, speech.FormatBytes(plan.NeedBytes()), speech.FormatBytes(free)))
		return nil
	}

	answer, err := s.readLine(fmt.Sprintf("Download %s now? [Y/n]: ", speech.FormatBytes(plan.DownloadBytes())), false)
	if err != nil {
		return err
	}
	if a := strings.ToLower(answer); a != "" && a != "y" && a != "yes" {
		fmt.Fprintln(s.out, indent+"Cancelled. Nothing was downloaded.")
		return nil
	}

	fmt.Fprintln(s.out, indent+s.st.dim("Press Ctrl+C to stop the download."))
	opCtx, done := s.ops.start(ctx)
	paths, err := s.audio.Install(opCtx, plan, func(p speech.Progress) { renderProgress(s.out, s.st, p) })
	done()
	if err != nil {
		fmt.Fprintln(s.out)
		if errors.Is(err, context.Canceled) {
			renderError(s.out, s.st, errors.New("Download stopped. The unfinished file was deleted; run /setup-audio to start again."))
		} else {
			renderError(s.out, s.st, err)
			fmt.Fprintln(s.out, indent+"Run /setup-audio to try again; finished parts are kept.")
		}
		return nil
	}

	fmt.Fprintln(s.out, indent+s.st.green("✓")+" Installed in "+filepath.Dir(paths.Server))
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+s.st.dim("Checking the new installation..."))
	r := s.audio.Status(ctx, true, func(note string) { fmt.Fprintln(s.out, indent+s.st.dim(note)) })
	s.renderReport(r)
	s.renderVerdict(r)
	return nil
}

func (s *session) askFolder(def string) (string, error) {
	for {
		line, err := s.readLine(fmt.Sprintf("Install folder [%s]: ", def), false)
		if err != nil {
			return "", err
		}
		if line == "" {
			return def, nil
		}
		line = strings.Trim(line, `"`)
		if !filepath.IsAbs(line) {
			renderError(s.out, s.st, errors.New(`Use a full path, e.g. D:\Tools\whisper`))
			continue
		}
		return filepath.Clean(line), nil
	}
}

func (s *session) renderReport(r speech.Report) {
	nameW := 0
	for _, c := range r.Checks {
		nameW = max(nameW, len(c.Name))
	}
	for _, c := range r.Checks {
		var mark string
		switch c.State {
		case speech.OK:
			mark = s.st.green("✓")
		case speech.Warn:
			mark = s.st.yellow("!")
		case speech.Fail:
			mark = s.st.red("✗")
		default:
			mark = s.st.dim("–")
		}
		fmt.Fprintf(s.out, "%s%s %s  %s\n", indent, mark, padRight(c.Name, nameW), c.Detail)
		if c.Fix != "" && c.State != speech.OK {
			fmt.Fprintf(s.out, "%s  %s  %s\n", indent, strings.Repeat(" ", nameW), s.st.dim("→ "+c.Fix))
		}
	}
}

func (s *session) renderVerdict(r speech.Report) {
	fmt.Fprintln(s.out)
	if r.Ready() {
		fmt.Fprintln(s.out, indent+s.st.bold(s.st.green("Audio-to-text: READY")))
		fmt.Fprintln(s.out, indent+"Type /audio to record entries by voice.")
		return
	}
	fmt.Fprintln(s.out, indent+s.st.bold(s.st.red("Audio-to-text: NOT READY")))
	for _, c := range r.Checks {
		if c.State == speech.Fail && c.Fix != "" {
			fmt.Fprintln(s.out, indent+"Next step: "+c.Fix)
			return
		}
	}
}

// renderProgress draws a download progress line in place; finished steps get their own line.
func renderProgress(w io.Writer, st styles, p speech.Progress) {
	const width = 78
	if p.Total <= 0 {
		if p.Finished {
			fmt.Fprintf(w, "\r%s\n", padRight(indent+st.green("✓")+" "+strings.TrimPrefix(p.Label, "Unpacking ")+" unpacked", width))
		} else {
			fmt.Fprintf(w, "\r%s", padRight(indent+"… "+p.Label+"...", width))
		}
		return
	}
	if p.Finished {
		label := strings.TrimPrefix(p.Label, "Downloading ")
		fmt.Fprintf(w, "\r%s\n", padRight(fmt.Sprintf("%s%s %s downloaded and verified (%s)", indent, st.green("✓"), label,
			speech.FormatBytes(p.Total)), width))
		return
	}
	const barW = 20
	frac := float64(p.Done) / float64(p.Total)
	filled := min(barW, int(frac*barW))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barW-filled)
	line := fmt.Sprintf("%s%s %s %s / %s %3.0f%% %s/s", indent, p.Label, bar,
		speech.FormatBytes(p.Done), speech.FormatBytes(p.Total), frac*100, speech.FormatBytes(int64(p.Rate)))
	fmt.Fprintf(w, "\r%s", padRight(line, width))
}
