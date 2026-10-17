package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"money-manager/internal/lineedit"
	"money-manager/internal/money"
	"money-manager/internal/store"
	"money-manager/internal/voice"
)

// VoiceInput starts the speech engine for /audio. Implemented by voice.Launcher.
type VoiceInput interface {
	Start(ctx context.Context) (voice.Engine, error)
}

// KeyReader reads single key presses ("enter", "esc", "ctrl+c", "eof" or a character).
// The line editor implements it; so does the plain reader (one line = one key).
type KeyReader interface {
	ReadKey() (string, error)
}

// Waker is implemented by key readers that can release a pending ReadKey (it then
// returns "wake"). Wake reports whether that is supported.
type Waker interface {
	Wake() bool
}

// ReadKey reads one line as one key. It writes nothing: it runs in a goroutine while the
// listening screen is drawn.
func (r *scannerReader) ReadKey() (string, error) {
	if !r.sc.Scan() {
		if err := r.sc.Err(); err != nil {
			return "eof", err
		}
		return "eof", io.EOF
	}
	if k := strings.TrimSpace(strings.ToLower(r.sc.Text())); k != "" {
		return k, nil
	}
	return "enter", nil
}

const audioKeysHint = `Enter or say "stop" / "berhenti": finish and review · Esc: cancel`

type listenResult int

const (
	listenFinished listenResult = iota
	listenCancelled
	listenFailed
)

func (s *session) audioMode(ctx context.Context) error {
	if s.voice == nil {
		renderError(s.out, s.st, errNoAudio)
		return nil
	}
	keys, ok := s.lines.(KeyReader)
	if !ok {
		renderError(s.out, s.st, errors.New("Voice input needs an interactive terminal."))
		return nil
	}
	ctx, done := s.ops.start(ctx)
	defer done()
	if _, err := s.reloadCategories(ctx); err != nil {
		renderError(s.out, s.st, fmt.Errorf("%v. Spoken categories won't be recognised.", err))
	}

	fmt.Fprintln(s.out)
	fmt.Fprint(s.out, indent+"Starting speech model...")
	eng, err := s.voice.Start(ctx)
	if err != nil {
		fmt.Fprintln(s.out)
		renderError(s.out, s.st, err)
		fmt.Fprintln(s.out, indent+"Run /audio-status to see what's missing.")
		return nil
	}
	defer eng.Close()
	where := "CPU"
	if eng.GPU() {
		where = "GPU"
	}
	fmt.Fprintf(s.out, " ready (%s).\n", where)
	fmt.Fprintln(s.out, indent+`Say one entry, then pause. Example: "income dua ratus ribu"`)
	fmt.Fprintln(s.out, indent+s.st.dim(audioKeysHint))
	fmt.Fprintln(s.out)

	var pending []voice.Draft
	for {
		res, err := s.listen(ctx, eng, keys, &pending)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			if res == listenFinished {
				fmt.Fprintln(s.out, indent+"No entries recorded.")
			} else {
				fmt.Fprintln(s.out, indent+"Stopped listening.")
			}
			return nil
		}
		if res == listenCancelled {
			answer, err := s.readLine(fmt.Sprintf("Stop and discard %s? [y/N]: ", entries(len(pending))), false)
			if err != nil {
				return err
			}
			if isYes(answer) {
				fmt.Fprintf(s.out, "%sDiscarded %s.\n", indent, entries(len(pending)))
				return nil
			}
		}
		more, err := s.review(ctx, &pending)
		if err != nil || !more {
			return err
		}
		fmt.Fprintln(s.out)
		fmt.Fprintln(s.out, indent+"Listening again. "+s.st.dim(audioKeysHint))
		fmt.Fprintln(s.out)
	}
}

type keyResult struct {
	key string
	err error
}

// listen runs one listening session, adding recognised entries to pending.
func (s *session) listen(ctx context.Context, eng voice.Engine, keys KeyReader, pending *[]voice.Draft) (listenResult, error) {
	l, err := eng.Listen(ctx)
	if err != nil {
		renderError(s.out, s.st, err)
		return listenFailed, nil
	}
	sl := &statusLine{out: s.out, st: s.st}
	keyCh := make(chan keyResult, 1)
	readKey := func() {
		go func() {
			k, err := keys.ReadKey()
			keyCh <- keyResult{k, err}
		}()
	}
	readKey()

	state, level, inflight := "calibrating", 0.0, 0
	redraw := func() {
		next := len(*pending) + inflight + 1
		var text string
		switch state {
		case "calibrating":
			text = s.st.dim("Calibrating the microphone, stay quiet for a moment...")
		case "recording":
			text = s.st.red("◉") + fmt.Sprintf(" Recording entry #%d...   ", next) + meterBar(level)
		default:
			text = s.st.green("●") + fmt.Sprintf(" Ready for entry #%d · speak now   ", next) + meterBar(level)
		}
		if inflight > 0 {
			text += s.st.dim(fmt.Sprintf("   understanding %d...", inflight))
		}
		sl.draw(text)
	}
	handle := func(ev voice.Event) {
		switch ev.Kind {
		case voice.Calibrating:
			state = "calibrating"
		case voice.Ready:
			state = "ready"
		case voice.Level:
			level = ev.Level
		case voice.Recording:
			state = "recording"
		case voice.Understanding:
			inflight++
		case voice.Heard:
			inflight = max(0, inflight-1)
			parts, _ := voice.Split(ev.Text)
			for _, p := range parts {
				s.heard(sl, voice.Event{Kind: voice.Heard, At: ev.At, Text: p}, pending)
			}
		case voice.Failed:
			if !ev.Fatal {
				inflight = max(0, inflight-1)
			}
			sl.println(indent + s.st.red("✗") + " " + ev.Err.Error())
		}
		redraw()
	}
	redraw()

	waitKey := func() {
		sl.println(indent + s.st.dim("Press any key to continue."))
		sl.clear()
		<-keyCh
	}
	// finish stops the microphone and waits for entries already spoken.
	finish := func() {
		sl.draw(s.st.dim("Finishing the last entry..."))
		l.Finish()
		for ev := range l.Events() {
			if ev.Kind == voice.Heard || ev.Kind == voice.Failed { // a stop word here is just dropped
				handle(ev)
				sl.draw(s.st.dim("Finishing the last entry..."))
			}
		}
		sl.clear()
	}
	for {
		select {
		case ev, ok := <-l.Events():
			if !ok {
				sl.clear()
				waitKey()
				return listenFailed, nil
			}
			if _, stop := voice.Split(ev.Text); ev.Kind == voice.Heard && stop {
				handle(ev) // entries said in the same breath as the stop word
				sl.println(fmt.Sprintf("%s%s heard %q: stopped listening.", indent, s.st.dim("■"), ev.Text))
				finish()
				// A key read is still waiting; release it before the review reads input.
				if w, ok := keys.(Waker); !ok || !w.Wake() {
					fmt.Fprintln(s.out, indent+s.st.dim("Press Enter to review."))
				}
				if k := <-keyCh; k.err != nil {
					return 0, k.err
				}
				return listenFinished, nil
			}
			handle(ev)
			if ev.Kind == voice.Failed && ev.Fatal {
				l.Close()
				sl.clear()
				waitKey()
				return listenFailed, nil
			}
		case k := <-keyCh:
			switch {
			case k.err != nil:
				l.Close()
				sl.clear()
				return 0, k.err
			case k.key == "enter":
				finish()
				return listenFinished, nil
			case k.key == "esc" || k.key == "ctrl+c":
				l.Close()
				sl.clear()
				return listenCancelled, nil
			default:
				readKey()
			}
		case <-ctx.Done():
			l.Close()
			sl.clear()
			<-keyCh
			return listenCancelled, nil
		}
	}
}

func (s *session) heard(sl *statusLine, ev voice.Event, pending *[]voice.Draft) {
	d, err := voice.Interpreter{Categories: s.cats}.Interpret(ev.Text, ev.At.In(s.loc()))
	switch {
	case errors.Is(err, voice.ErrNothingHeard):
		return
	case err != nil:
		sl.println(fmt.Sprintf("%s%s     heard %q: %v", indent, s.st.red("✗"), ev.Text, err))
		return
	}
	*pending = append(*pending, d)
	category, note := "", ""
	if d.Category != "" {
		category = d.Category + " · "
	}
	if d.Note != "" {
		note = shortNote(d.Note) + "   "
	}
	sl.println(fmt.Sprintf("%s%s #%-3d %s %s · %s%s   %s%s", indent, s.st.green("✓"), len(*pending),
		s.st.kind(d.Kind, padRight(string(d.Kind), 7)), padLeft(money.Format(d.Amount), 14),
		category, d.At.Format("15:04"), note, s.st.dim(fmt.Sprintf("heard %q", d.Heard))))
}

// review shows all pending entries and handles t / d / r / y / n. It reports whether the
// user wants to record more.
func (s *session) review(ctx context.Context, pending *[]voice.Draft) (bool, error) {
	s.renderPending(*pending)
	for {
		line, err := s.readLine("review> ", false)
		if errors.Is(err, lineedit.ErrInterrupt) {
			answer, err := s.readLine(fmt.Sprintf("Discard %s and quit? [y/N]: ", entries(len(*pending))), false)
			if err != nil || isYes(answer) {
				return false, lineedit.ErrInterrupt
			}
			continue
		}
		if err != nil {
			return false, err
		}
		fields := strings.Fields(strings.ToLower(line))
		if len(fields) == 0 {
			s.reviewOptions()
			continue
		}
		cmd, arg := fields[0], ""
		if len(fields) > 1 {
			arg = fields[1]
		}
		switch {
		case cmd == "t" && arg == "":
			for i := range *pending {
				if err := s.editTime(&(*pending)[i], i); err != nil {
					return false, err
				}
			}
			s.renderPending(*pending)
		case cmd == "t" || cmd == "d" || cmd == "c" || cmd == "k":
			i, err := entryIndex(arg, len(*pending))
			if err != nil {
				renderError(s.out, s.st, err)
				continue
			}
			switch cmd {
			case "t":
				if err := s.editTime(&(*pending)[i], i); err != nil {
					return false, err
				}
			case "c":
				d := &(*pending)[i]
				fmt.Fprintf(s.out, "%s#%d %s %s\n", indent, i+1, d.Kind, money.Format(d.Amount))
				if d.Note, err = s.askNote(d.Note); err != nil {
					return false, err
				}
			case "k":
				d := &(*pending)[i]
				fmt.Fprintf(s.out, "%s#%d %s %s\n", indent, i+1, d.Kind, money.Format(d.Amount))
				c, err := s.askCategory(ctx, d.Kind, store.Category{ID: d.CategoryID, Kind: d.Kind, Name: d.Category})
				if err != nil {
					return false, err
				}
				d.CategoryID, d.Category = c.ID, c.Name
			default:
				*pending = append((*pending)[:i], (*pending)[i+1:]...)
				fmt.Fprintf(s.out, "%s%s Removed #%d.\n", indent, s.st.green("✓"), i+1)
				if len(*pending) == 0 {
					fmt.Fprintln(s.out, indent+"No entries left. Nothing to save.")
					return false, nil
				}
			}
			s.renderPending(*pending)
		case cmd == "r":
			return true, nil
		case cmd == "y":
			rows := make([]store.NewTransaction, len(*pending))
			for i, d := range *pending {
				rows[i] = store.NewTransaction{Kind: d.Kind, Amount: d.Amount, OccurredAt: d.At, Note: d.Note, CategoryID: d.CategoryID}
			}
			if _, err := s.store.AddTransactions(ctx, rows); err != nil {
				if errors.Is(err, store.ErrCategoryKind) {
					s.reloadCategories(ctx)
					err = errors.New("a category was removed elsewhere; change it with k <#>")
				}
				renderError(s.out, s.st, fmt.Errorf("Couldn't save: %v. Nothing was saved; the entries are still here.", err))
				continue
			}
			fmt.Fprintf(s.out, "%s%s Saved %s. Recordings cleared from memory.\n", indent, s.st.green("✓"), entries(len(rows)))
			*pending = nil
			return false, nil
		case cmd == "n":
			answer, err := s.readLine(fmt.Sprintf("Discard %s? [y/N]: ", entries(len(*pending))), false)
			if err != nil {
				return false, err
			}
			if isYes(answer) {
				fmt.Fprintf(s.out, "%sDiscarded %s.\n", indent, entries(len(*pending)))
				*pending = nil
				return false, nil
			}
		default:
			renderError(s.out, s.st, errors.New("Type t, t <#>, k <#>, c <#>, d <#>, r, y or n."))
		}
	}
}

func (s *session) editTime(d *voice.Draft, i int) error {
	prompt := fmt.Sprintf("#%d %s %s    Date & time [%s]: ", i+1, padRight(string(d.Kind), 7),
		padLeft(money.Format(d.Amount), 14), d.At.Format(timeLayout))
	for {
		line, err := s.readLine(prompt, false)
		if err != nil {
			return err
		}
		if line == "" {
			return nil
		}
		t, err := ParseEditTime(line, d.At)
		if err != nil {
			renderError(s.out, s.st, err)
			continue
		}
		d.At = t
		return nil
	}
}

func (s *session) renderPending(pending []voice.Draft) {
	const maxHeard = 36
	heard := func(d voice.Draft) string {
		h := d.Heard
		if utf8.RuneCountInString(h) > maxHeard {
			h = string([]rune(h)[:maxHeard-1]) + "…"
		}
		return h
	}
	heardW := len("Heard")
	amtW := len("Amount")
	var income, expenses int64
	changed := false
	for _, d := range pending {
		heardW = max(heardW, utf8.RuneCountInString(heard(d)))
		amtW = max(amtW, len(money.Format(d.Amount)))
		if d.Kind == store.Income {
			income += d.Amount
		} else {
			expenses += d.Amount
		}
		changed = changed || !d.At.Equal(d.Recorded)
	}
	numW := len(strconv.Itoa(len(pending)))
	sep := "   "
	notes := make([]string, len(pending))
	cats := make([]string, len(pending))
	for i, d := range pending {
		notes[i], cats[i] = d.Note, d.Category
	}
	cc := newCategoryColumnOf(cats, sep)
	nc := newNoteColumnOf(notes, " ")
	const dateW = len(timeLayout) + 2 // room for the " *" mark
	rowW := numW + 3*len(sep) + heardW + 7 + cc.width() + amtW + 2 + dateW + nc.width()
	rule := indent + s.st.dim(strings.Repeat("─", rowW))

	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+s.st.bold("Recorded entries (NOT saved yet)"))
	fmt.Fprintln(s.out, rule)
	fmt.Fprintln(s.out, indent+" "+s.st.dim(padRight("#", numW)+sep+padRight("Heard", heardW)+sep+
		padRight("Type", 7)+sep+cc.header()+padLeft("Amount", amtW)+"  "+padRight("Date & time", dateW)+nc.header()))
	for i, d := range pending {
		date := d.At.Format(timeLayout)
		if !d.At.Equal(d.Recorded) {
			date += " *"
		}
		if nc.w > 0 {
			date = padRight(date, dateW)
		}
		fmt.Fprintln(s.out, indent+" "+padRight(strconv.Itoa(i+1), numW)+sep+padRight(heard(d), heardW)+sep+
			s.st.kind(d.Kind, padRight(string(d.Kind), 7))+sep+cc.cell(d.Category)+padLeft(money.Format(d.Amount), amtW)+"  "+
			date+nc.cell(d.Note))
	}
	fmt.Fprintln(s.out, rule)
	fmt.Fprintf(s.out, "%s Income %s · Expenses %s\n", indent, s.st.green(money.Format(income)), s.st.red(money.Format(expenses)))
	if changed {
		fmt.Fprintln(s.out, indent+s.st.dim(" * changed by you; the others keep the time they were recorded."))
	} else {
		fmt.Fprintln(s.out, indent+s.st.dim(" Date & time is when each entry was recorded, unless you change it."))
	}
	s.reviewOptions()
}

func (s *session) reviewOptions() {
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+"t: set date & time of every entry · t <#>: one entry · k <#>: category · c <#>: note")
	fmt.Fprintln(s.out, indent+"d <#>: delete · r: record more · y: save all · n: discard all")
}

func entryIndex(arg string, n int) (int, error) {
	i, err := strconv.Atoi(strings.TrimPrefix(arg, "#"))
	if err != nil || i < 1 || i > n {
		return 0, fmt.Errorf("Choose an entry number from 1 to %d, e.g. d 1", n)
	}
	return i - 1, nil
}

func entries(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}

func isYes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes" || s == "ya"
}

func meterBar(level float64) string {
	const w = 10
	n := min(w, max(0, int(level*w+0.5)))
	return strings.Repeat("█", n) + strings.Repeat("░", w-n)
}

// statusLine is one line at the bottom that is redrawn in place; permanent lines are
// printed above it.
type statusLine struct {
	out     io.Writer
	st      styles
	text    string
	visible bool
}

func (l *statusLine) draw(text string) {
	l.text = text
	l.visible = true
	if l.st.on {
		fmt.Fprint(l.out, "\r"+indent+text+"\x1b[K")
	} else {
		fmt.Fprint(l.out, "\r"+padRight(indent+text, 78))
	}
}

func (l *statusLine) clear() {
	if !l.visible {
		return
	}
	l.visible = false
	if l.st.on {
		fmt.Fprint(l.out, "\r\x1b[K")
	} else {
		fmt.Fprint(l.out, "\r"+strings.Repeat(" ", 78)+"\r")
	}
}

func (l *statusLine) println(line string) {
	wasVisible := l.visible
	l.clear()
	fmt.Fprintln(l.out, line)
	if wasVisible {
		l.draw(l.text)
	}
}
