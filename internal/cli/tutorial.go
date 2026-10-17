package cli

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"money-manager/internal/store"
)

type tutorialStep struct {
	title string
	body  []string
	try   func(now time.Time) string // suggested command; nil for read-only steps
	done  string                     // shown after the user runs the suggested kind of command
}

var tutorialSteps = []tutorialStep{
	{
		title: "Welcome",
		body: []string{
			"Money Manager keeps track of money coming in (income) and",
			"money going out (expenses), and shows a summary for any month.",
			"",
			"This is practice mode: nothing you type here is saved.",
		},
	},
	{
		title: "Record income",
		body: []string{
			"When you receive money, type income followed by the amount.",
			"Commas and up to 2 decimals are fine: 5000000, 5,000,000, 5000000.50",
		},
		try:  func(time.Time) string { return "income 5000000" },
		done: "Nice! Without a date, the current date and time are used.",
	},
	{
		title: "Record an expense",
		body: []string{
			"When you spend money, type expenses followed by the amount.",
			"expense (without the s) works too.",
		},
		try:  func(time.Time) string { return "expenses 25000.50" },
		done: "Saved. Amounts are always positive; the type says which way the money went.",
	},
	{
		title: "Add date and time",
		body: []string{
			"Forgot to record something? Add the date and time after the amount:",
			"YYYY-MM-DD HH:MM, 24-hour clock. Give both, or neither.",
		},
		try: func(now time.Time) string {
			return fmt.Sprintf("expenses 150000 %04d-%02d-01 19:30", now.Year(), now.Month())
		},
		done: "That entry is filed under the date you gave, not today.",
	},
	{
		title: "See a month",
		body: []string{
			"list <month> <year> shows every entry in that month, then total",
			"income, total expenses and net (income minus expenses).",
			"Type just list and press Enter twice to see the current month.",
		},
		try: func(now time.Time) string {
			return fmt.Sprintf("list %d %d", now.Month(), now.Year())
		},
		done: "Those are the entries you practiced in this tutorial.",
	},
	{
		title: "Fix a mistake",
		body: []string{
			"delete shows today's entries; type a number (or several: 1 3) and confirm.",
			"p / n go to the previous / next day, or type a date like 2026-10-15.",
			"edit works the same way and asks what to change.",
			"Deleted entries stay in trash for 30 days; undo reverses your last change.",
		},
		try:  func(time.Time) string { return "delete" },
		done: "Type undo to bring it back, or trash to see deleted entries.",
	},
	{
		title: "You're set",
		body: []string{
			"/help           show all commands",
			"/tutorial       run this guide again",
			"/audio          speak entries, e.g. \"income dua ratus ribu\"",
			"/setup-audio    set up voice input (audio-to-text)",
			"/exit           quit (or Ctrl+C)",
			"",
			"Tips: start typing and press Tab to complete a command (in → income),",
			"type / to see every command, and press the up arrow for previous lines.",
		},
	},
}

const tutorialControls = "Enter: next · b: back · q: quit tutorial"

// runTutorial walks through tutorialSteps. Commands run against a fresh in-memory
// store so nothing reaches the real database. It returns io.EOF if input ends.
func runTutorial(ctx context.Context, real *session) error {
	s := *real
	s.store = store.NewMemory()
	s.practice = true
	s.lastChange = nil // undo in the tutorial must never touch real entries

	for i := 0; i < len(tutorialSteps); {
		step := tutorialSteps[i]
		renderStep(&s, i, step)

		next, err := tutorialStepLoop(ctx, &s, step)
		if err != nil {
			return err
		}
		switch next {
		case stepNext:
			i++
		case stepBack:
			i = max(0, i-1)
		case stepQuit:
			i = len(tutorialSteps)
		}
	}
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+s.st.bold("Tutorial finished.")+" Back to your real data.")
	return nil
}

type stepAction int

const (
	stepNext stepAction = iota
	stepBack
	stepQuit
)

func renderStep(s *session, i int, step tutorialStep) {
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+s.st.bold(fmt.Sprintf("Tutorial · %d/%d · %s", i+1, len(tutorialSteps), step.title)))
	fmt.Fprintln(s.out, indent+s.st.dim(strings.Repeat("─", 48)))
	for _, l := range step.body {
		fmt.Fprintln(s.out, indent+l)
	}
	if step.try != nil {
		fmt.Fprintln(s.out)
		fmt.Fprintln(s.out, indent+"Try it:  "+s.st.bold(step.try(s.now())))
	}
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+s.st.dim(tutorialControls))
}

func tutorialStepLoop(ctx context.Context, s *session, step tutorialStep) (stepAction, error) {
	var suggested Command
	if step.try != nil {
		suggested, _ = Parse(step.try(s.now()), s.loc())
	}
	for {
		line, err := s.readLine("tutorial> ", true)
		if err != nil {
			return 0, err
		}
		switch strings.ToLower(line) {
		case "":
			return stepNext, nil
		case "b", "back":
			return stepBack, nil
		}

		cmd, err := Parse(line, s.loc())
		if err != nil {
			renderError(s.out, s.st, err)
			if step.try != nil {
				fmt.Fprintln(s.out, indent+s.st.dim("Hint: try ")+step.try(s.now()))
			}
			continue
		}
		switch c := cmd.(type) {
		case ExitCmd:
			return stepQuit, nil
		case TutorialCmd:
			fmt.Fprintln(s.out, indent+"You're already in the tutorial.")
		default:
			if err := s.exec(ctx, c); err != nil {
				return 0, err
			}
			if step.done != "" && sameKind(c, suggested) {
				fmt.Fprintln(s.out, indent+s.st.green(step.done))
			}
		}
		fmt.Fprintln(s.out)
		fmt.Fprintln(s.out, indent+s.st.dim(tutorialControls))
	}
}

// sameKind reports whether cmd is the kind of command the step asked for
// (e.g. any expense on the expense step), ignoring the exact values.
func sameKind(cmd, suggested Command) bool {
	if suggested == nil || reflect.TypeOf(cmd) != reflect.TypeOf(suggested) {
		return false
	}
	a, ok := cmd.(AddCmd)
	if !ok {
		return true
	}
	b := suggested.(AddCmd)
	return a.Kind == b.Kind && a.At.IsZero() == b.At.IsZero()
}
