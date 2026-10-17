package cli

import (
	"strings"
	"testing"

	"money-manager/internal/store"
)

func TestTutorialFullRun(t *testing.T) {
	real := store.NewMemory()
	out := run(t, real, strings.Join([]string{
		"/tutorial",
		"",                                 // 1 Welcome -> next
		"income 5000000",                   // 2 try
		"",                                 //   next
		"expenses 25000.50",                // 3 try
		"",                                 //   next
		"expenses 150000 2026-10-01 19:30", // 4 try
		"",                                 //   next
		"list 10 2026",                     // 5 try
		"",                                 //   next
		"delete",                           // 6 try: opens today's practice entries
		"2",                                //   pick the expense
		"y",                                //   confirm
		"",                                 //   leave the day view
		"undo",                             //   bring it back
		"",                                 //   confirm undo
		"",                                 //   next
		"",                                 // 7 -> finish
		"exit",
	}, "\n"))

	assertContains(t, out,
		"Tutorial · 1/7 · Welcome",
		"Tutorial · 2/7 · Record income",
		"Try it:  income 5000000",
		"✓ Saved income 5,000,000.00 · 2026-10-17 14:32   (practice, not saved)",
		"Nice! Without a date, the current date and time are used.",
		"Try it:  expenses 150000 2026-10-01 19:30",
		"Try it:  list 10 2026",
		"Those are the entries you practiced in this tutorial.",
		"Tutorial · 6/7 · Fix a mistake",
		"Try it:  delete",
		"Saturday, 17 October 2026",
		"#2  expense 25,000.50 · 2026-10-17 14:32",
		"✓ Deleted 1 entry.",
		"Type undo to bring it back, or trash to see deleted entries.",
		"✓ Undone.",
		"Tutorial · 7/7 · You're set",
		"Tutorial finished. Back to your real data.",
	)

	listing := out[strings.Index(out, "October 2026"):]
	assertContains(t, listing, "5,000,000.00", "25,000.50", "150,000.00", "4,824,999.50")

	if real.Len() != 0 {
		t.Errorf("tutorial wrote %d rows to the real store", real.Len())
	}
}

func TestTutorialInvalidInputShowsHint(t *testing.T) {
	out := run(t, store.NewMemory(), "/tutorial\n\nincome\nq\n")
	assertContains(t, out,
		"✗ Missing amount.",
		"Hint: try income 5000000",
		"Tutorial finished.")
}

func TestTutorialBackAndQuit(t *testing.T) {
	out := run(t, store.NewMemory(), "/tutorial\n\nb\n/exit\nincome 1\n")
	if n := strings.Count(out, "Tutorial · 1/7"); n != 2 {
		t.Errorf("step 1 shown %d times, want 2 (after going back)", n)
	}
	// /exit inside the tutorial leaves the tutorial, not the app.
	assertContains(t, out, "Tutorial finished.", "✓ Saved income 1.00")
}

func TestTutorialOtherCommandDoesNotPraise(t *testing.T) {
	out := run(t, store.NewMemory(), "/tutorial\n\n\nincome 100\nq\n")
	assertContains(t, out, "Tutorial · 3/7", "✓ Saved income 100.00")
	if strings.Contains(out, "Saved. Amounts are always positive") {
		t.Error("expense-step message shown for an income entry")
	}
}

func TestTutorialEOF(t *testing.T) {
	out := run(t, store.NewMemory(), "/tutorial\n")
	assertContains(t, out, "Tutorial · 1/7", "Bye!")
}
