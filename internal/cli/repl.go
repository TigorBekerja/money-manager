// Package cli implements the interactive session: parsing, executing and rendering
// commands, and the /tutorial walkthrough.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"money-manager/internal/lineedit"
	"money-manager/internal/store"
)

// Store is implemented by store.Postgres and store.Memory.
type Store interface {
	AddTransaction(ctx context.Context, v store.NewTransaction) (store.Transaction, error)
	AddTransactions(ctx context.Context, rows []store.NewTransaction) ([]store.Transaction, error)
	ListBetween(ctx context.Context, from, to time.Time) ([]store.Transaction, error)
	Nearest(ctx context.Context, t time.Time, before bool) (time.Time, bool, error)
	UpdateTransaction(ctx context.Context, id int64, v store.NewTransaction, expectUpdatedAt time.Time) (store.Transaction, error)
	DeleteTransactions(ctx context.Context, ids []int64) ([]store.Transaction, error)
	RestoreTransactions(ctx context.Context, ids []int64) ([]store.Transaction, error)
	ListDeleted(ctx context.Context) ([]store.Transaction, error)
	Categories(ctx context.Context) ([]store.Category, error)
	AddCategory(ctx context.Context, kind store.Kind, name string) (store.Category, error)
	RenameCategory(ctx context.Context, id int64, name string) (store.Category, error)
	RemoveCategory(ctx context.Context, id, moveTo int64) (int, error)
	CategoryCounts(ctx context.Context) (map[int64]int, error)
}

// LineReader reads one line of input. With complete set, command suggestions, Tab
// completion and history are offered (if the implementation supports them). It returns
// io.EOF at end of input and lineedit.ErrInterrupt on Ctrl+C.
type LineReader interface {
	ReadLine(prompt string, complete bool) (string, error)
}

type REPL struct {
	In     io.Reader  // used when Lines is nil
	Lines  LineReader // optional; e.g. a *lineedit.Editor on an interactive terminal
	Out    io.Writer
	Store  Store
	Now    func() time.Time // its Location is the user's time zone
	Color  bool
	Banner BannerInfo
	Audio  AudioTools // optional; enables /audio-status and /setup-audio
	Voice  VoiceInput // optional; enables /audio

	ops ops
	sug suggester
}

// Suggest is Suggest with the categories of the running session, for completing a
// category after the amount. Use it as the line editor's Suggest.
func (r *REPL) Suggest(line string) lineedit.Suggestions { return suggest(line, r.sug.cats) }

// Interrupt cancels the running long operation (a download), if any. It reports
// whether there was one, so the caller can otherwise treat Ctrl+C as quit.
func (r *REPL) Interrupt() bool { return r.ops.interrupt() }

// Run shows the banner and processes commands until /exit, Ctrl+C or end of input.
func (r *REPL) Run(ctx context.Context) error {
	lines := r.Lines
	if lines == nil {
		lines = &scannerReader{sc: bufio.NewScanner(r.In), out: r.Out}
	}
	s := &session{
		lines: lines,
		out:   r.Out,
		store: r.Store,
		now:   r.Now,
		st:    styles{on: r.Color},
		audio: r.Audio,
		voice: r.Voice,
		ops:   &r.ops,
		sug:   &r.sug,
	}
	renderBanner(s.out, s.st, r.Banner)
	s.categories(ctx) // for suggestions; errors show up when categories are used

	for {
		line, err := s.readLine("> ", true)
		if err != nil {
			return s.bye(err)
		}
		cmd, err := Parse(line, s.loc())
		if err != nil {
			renderError(s.out, s.st, err)
			s.blank()
			continue
		}
		switch c := cmd.(type) {
		case nil:
			continue
		case ExitCmd:
			return s.bye(nil)
		case TutorialCmd:
			err := runTutorial(ctx, s)
			s.reloadCategories(ctx) // the tutorial's practice categories are gone
			if err != nil {
				return s.bye(err)
			}
		default:
			if err := s.exec(ctx, c); err != nil {
				return s.bye(err)
			}
		}
		s.blank()
	}
}

// session holds the I/O and store shared by the REPL and the tutorial.
type session struct {
	lines      LineReader
	out        io.Writer
	store      Store
	now        func() time.Time
	st         styles
	practice   bool
	lastChange *change // for undo
	audio      AudioTools
	voice      VoiceInput
	ops        *ops
	cats       []store.Category // cache; nil = not loaded (see categories)
	sug        *suggester
}

func (s *session) loc() *time.Location { return s.now().Location() }

func (s *session) blank() { fmt.Fprintln(s.out) }

// bye ends the session. End of input and Ctrl+C are normal exits, not errors.
func (s *session) bye(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, lineedit.ErrInterrupt) {
		err = nil
	}
	fmt.Fprintln(s.out, indent+"Bye!")
	return err
}

func (s *session) readLine(prompt string, complete bool) (string, error) {
	line, err := s.lines.ReadLine(indent+prompt, complete)
	return strings.TrimSpace(line), err
}

// scannerReader reads plain lines, for piped input and tests.
type scannerReader struct {
	sc  *bufio.Scanner
	out io.Writer
}

func (r *scannerReader) ReadLine(prompt string, _ bool) (string, error) {
	fmt.Fprint(r.out, prompt)
	if !r.sc.Scan() {
		if err := r.sc.Err(); err != nil {
			return "", err
		}
		fmt.Fprintln(r.out)
		return "", io.EOF
	}
	// Windows PowerShell prefixes piped input with a UTF-8 BOM.
	return strings.TrimPrefix(r.sc.Text(), "\xef\xbb\xbf"), nil
}

// exec runs add, list, help and the audio commands. Only input errors (end of input,
// Ctrl+C at a prompt) are returned; other errors are shown.
func (s *session) exec(ctx context.Context, cmd Command) error {
	switch c := cmd.(type) {
	case EditCmd:
		return s.dayView(ctx, pickEdit, c.Day)
	case DeleteCmd:
		return s.dayView(ctx, pickDelete, c.Day)
	case TrashCmd:
		return s.trashView(ctx)
	case UndoCmd:
		return s.undo(ctx)
	case AudioCmd, AudioStatusCmd, SetupAudioCmd:
		if s.practice {
			fmt.Fprintln(s.out, indent+"That works outside the tutorial. Try it after you finish.")
			return nil
		}
		switch c.(type) {
		case AudioCmd:
			return s.audioMode(ctx)
		case SetupAudioCmd:
			return s.setupAudio(ctx)
		}
		return s.audioStatus(ctx)
	case CategoriesCmd:
		return s.showCategories(ctx)
	case CategoryCmd:
		return s.categoryCmd(ctx, c)
	case AddCmd:
		cat, err := s.categoryOf(ctx, c)
		if err != nil {
			renderError(s.out, s.st, err)
			return nil
		}
		at := c.At
		if at.IsZero() {
			at = s.now().Truncate(time.Minute)
		}
		tx, err := s.store.AddTransaction(ctx, store.NewTransaction{Kind: c.Kind, Amount: c.Amount, OccurredAt: at, Note: c.Note, CategoryID: cat.ID})
		switch {
		case errors.Is(err, store.ErrCategoryKind):
			s.reloadCategories(ctx)
			renderError(s.out, s.st, fmt.Errorf("The category %s was just removed elsewhere. Nothing was saved.", cat.Name))
			return nil
		case err != nil:
			renderError(s.out, s.st, fmt.Errorf("Couldn't save: %v", err))
			return nil
		}
		renderSaved(s.out, s.st, tx, s.loc(), s.practice)
	case ListCmd:
		if c.Prompt {
			var err error
			if c, err = s.promptMonth(); err != nil {
				return err
			}
		}
		from := time.Date(c.Year, c.Month, 1, 0, 0, 0, 0, s.loc())
		txs, err := s.store.ListBetween(ctx, from, from.AddDate(0, 1, 0))
		if err != nil {
			renderError(s.out, s.st, fmt.Errorf("Couldn't load transactions: %v", err))
			return nil
		}
		renderMonth(s.out, s.st, c.Year, c.Month, txs, s.loc())
	case HelpCmd:
		renderHelp(s.out, s.st)
	}
	return nil
}

// promptMonth asks for month and year, defaulting to the current ones on an empty answer.
func (s *session) promptMonth() (ListCmd, error) {
	now := s.now()
	m, err := askUntilValid(s, fmt.Sprintf("Month (1-12) [%d]: ", now.Month()), now.Month(), ParseMonth)
	if err != nil {
		return ListCmd{}, err
	}
	y, err := askUntilValid(s, fmt.Sprintf("Year [%d]: ", now.Year()), now.Year(), ParseYear)
	if err != nil {
		return ListCmd{}, err
	}
	return ListCmd{Month: m, Year: y}, nil
}

func askUntilValid[T any](s *session, prompt string, def T, parse func(string) (T, error)) (T, error) {
	for {
		line, err := s.readLine(prompt, false)
		if err != nil {
			return def, err
		}
		if line == "" {
			return def, nil
		}
		v, err := parse(line)
		if err == nil {
			return v, nil
		}
		renderError(s.out, s.st, err)
	}
}
