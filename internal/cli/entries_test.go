package cli

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"money-manager/internal/store"
)

// fixedNow is Saturday 2026-10-17 14:32 WIB.
func seeded(t *testing.T) *store.Memory {
	t.Helper()
	mem := store.NewMemory()
	at := func(y int, m time.Month, d, hh, mm int) time.Time { return time.Date(y, m, d, hh, mm, 0, 0, wib) }
	_, err := mem.AddTransactions(context.Background(), []store.NewTransaction{
		{Kind: store.Income, Amount: 40_000, OccurredAt: at(2026, 10, 9, 8, 0)},
		{Kind: store.Expense, Amount: 15_000_000, OccurredAt: at(2026, 10, 15, 19, 30)},
		{Kind: store.Income, Amount: 500_000_000, OccurredAt: at(2026, 10, 17, 9, 0)},
		{Kind: store.Expense, Amount: 2_500_050, OccurredAt: at(2026, 10, 17, 14, 33)},
		{Kind: store.Expense, Amount: 100_000, OccurredAt: at(2026, 11, 2, 10, 0)},
		{Kind: store.Income, Amount: 700_000, OccurredAt: at(2025, 3, 4, 12, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return mem
}

func day(t *testing.T, mem *store.Memory, y int, m time.Month, d int) []store.Transaction {
	t.Helper()
	from := time.Date(y, m, d, 0, 0, 0, 0, wib)
	rows, err := mem.ListBetween(context.Background(), from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestDeleteFromDayView(t *testing.T) {
	mem := seeded(t)
	out := run(t, mem, strings.Join([]string{
		"delete",
		"2", "", // Enter = No
		"1 2 2", "y", // several, duplicate ignored
		"p", "n", "n", "n",
		"",
	}, "\n"))
	assertContains(t, out,
		"Saturday, 17 October 2026",
		"1   09:00   income    5,000,000.00",
		"2   14:33   expense      25,000.50",
		"Income 5,000,000.00 · Expenses 25,000.50",
		"p: previous day, Thu 15 Oct · n: next day, Mon 2 Nov · or type a date: 2026-10-15 · 2026-09 · 2025",
		"Choose entry number(s) to delete, e.g. 2 or 1 3 · Enter: done",
		"#2  expense 25,000.50 · 2026-10-17 14:33",
		"Delete this entry? It stays in trash for 30 days. [y/N]: ",
		"Nothing deleted.",
		"Delete these 2 entries? They stay in trash for 30 days. [y/N]: ",
		"✓ Deleted 2 entries. Type undo to bring it back, or find it in trash.",
		"No entries today. Press p to go to the last day with entries.",
		"Thursday, 15 October 2026",
		"Monday, 2 November 2026", // n skips the now-empty 17th
		"No later entries.",
		"p: previous day, Thu 15 Oct · no later entries · or type", // on 2 Nov
	)
	if n := len(day(t, mem, 2026, 10, 17)); n != 0 {
		t.Errorf("%d entries left on the 17th", n)
	}
	if trash, _ := mem.ListDeleted(context.Background()); len(trash) != 2 {
		t.Errorf("trash has %d entries, want 2", len(trash))
	}
}

func TestDayViewNavigation(t *testing.T) {
	out := run(t, seeded(t), strings.Join([]string{
		"delete 2026-10-15",
		"2026-09", "2025", "2026-10", "2026-10-16", "2026-13", "2026-02-30", "foo", "3",
		"p", "p", "p", "p", // 15 Oct, 9 Oct, 4 Mar 2025, then nothing earlier
		"",
	}, "\n"))
	assertContains(t, out,
		"Thursday, 15 October 2026",
		"No entries in September 2026.",
		"Tuesday, 4 March 2025",
		"Friday, 9 October 2026",
		"Friday, 16 October 2026",
		"No entries on this day.",
		`✗ Invalid month "2026-13"`,
		`✗ Invalid date "2026-02-30"`,
		"✗ Type p, n, a date (2026-10-15), a month (2026-09), a year (2025), or entry number(s).",
		"✗ There are no entries on this day.",
		"No earlier entries.",
		// The neighbours with entries are named; another year shows the year.
		"p: previous day, Thu 15 Oct · n: next day, Sat 17 Oct · or type",     // on the empty 16th
		"p: previous day, Tue 4 Mar 2025 · n: next day, Thu 15 Oct · or type", // on 9 Oct
		"no earlier entries · n: next day, Fri 9 Oct 2026 · or type",          // on 4 Mar 2025
	)
	if strings.Count(out, "Tuesday, 4 March 2025") < 2 {
		t.Error("p from 16 Oct should reach 15 Oct, 9 Oct, then 4 March 2025")
	}
}

func TestEditByQuestions(t *testing.T) {
	mem := seeded(t)
	out := run(t, mem, strings.Join([]string{
		"edit",
		"2",
		"foo", "income", // type: invalid, then income
		"",             // category: Enter = none
		"abc", "27500", // amount: invalid, then 27500
		"2026-10-16", // date only: keeps 14:33
		"",           // note: Enter = none
		"",           // Save? Enter = Yes
		"",
	}, "\n"))
	assertContains(t, out,
		"Choose an entry number to edit, e.g. 2 (or 2 expense 27500 food 2026-10-16 14:33) · Enter: done",
		"#2  expense 25,000.50 · 2026-10-17 14:33",
		"Type [expense]: ",
		"✗ Type income or expenses",
		"Amount [25000.50]: ",
		"✗ Amount must be a positive number",
		"Date & time [2026-10-17 14:33]: ",
		"Change #2:",
		"type         expense  →  income",
		"amount       25,000.50  →  27,500.00",
		"date & time  2026-10-17 14:33  →  2026-10-16 14:33",
		"Save? [Y/n]: ",
		"✓ Updated. It moved to Friday, 16 October 2026.",
	)
	rows := day(t, mem, 2026, 10, 16)
	if len(rows) != 1 || rows[0].Kind != store.Income || rows[0].Amount != 2_750_000 ||
		!rows[0].OccurredAt.Equal(time.Date(2026, 10, 16, 14, 33, 0, 0, wib)) || rows[0].UpdatedAt.IsZero() {
		t.Errorf("edited row = %+v", rows)
	}
}

func TestEditShortcutAndNoChanges(t *testing.T) {
	mem := seeded(t)
	out := run(t, mem, strings.Join([]string{
		"edit",
		"1 2",                   // two numbers
		"2 pengeluaran 30000",   // shortcut, Indonesian type word, keeps date/time
		"",                      // Save
		"1", "", "", "", "", "", // Enter keeps everything (type, category, amount, date, note)
		"2 expense 1 2026-10-17 25:00", // invalid time in the shortcut
		"2 expense 1", "n",             // declined
		"",
	}, "\n"))
	assertContains(t, out,
		"✗ Choose one entry to edit.",
		"amount       25,000.50  →  30,000.00",
		"✓ Updated.",
		"No changes.",
		`✗ Invalid time "25:00"`,
		"Nothing changed.",
	)
	if strings.Contains(out, "type         expense") {
		t.Error("unchanged type listed as a change")
	}
	rows := day(t, mem, 2026, 10, 17)
	if rows[1].Amount != 3_000_000 || rows[1].Kind != store.Expense || rows[1].OccurredAt.Minute() != 33 {
		t.Errorf("row = %+v", rows[1])
	}
}

func TestDeleteRejectsEditLine(t *testing.T) {
	out := run(t, seeded(t), "delete\n2 expense 5\n9\n\n")
	assertContains(t, out, "✗ To change an entry, use edit.", "✗ Choose entry numbers from 1 to 2.")
}

func TestTrashAndRestore(t *testing.T) {
	mem := seeded(t)
	out := run(t, mem, strings.Join([]string{
		"trash", // empty
		"delete", "1 2", "y", "",
		"trash",
		"9",     // out of range
		"1", "", // restore #1 (newest deletion first: same time, lower id first)
		"1", "y", // restore the last one
		"list 10 2026",
	}, "\n"))
	assertContains(t, out,
		"The trash is empty.",
		"Deleted entries (kept 30 days)",
		"Removed on",
		"✗ Choose entry number(s) to restore",
		"Restore #1 income 5,000,000.00 · 2026-10-17 09:00? [Y/n]: ",
		"✓ Restored 1 entry.",
		"The trash is now empty.",
	)
	if n := len(day(t, mem, 2026, 10, 17)); n != 2 {
		t.Errorf("%d entries on the 17th after restoring, want 2", n)
	}
}

func TestUndo(t *testing.T) {
	mem := seeded(t)
	out := run(t, mem, strings.Join([]string{
		"undo", // nothing yet
		"edit", "2 expense 99", "", "",
		"undo", "n", // declined: still undoable
		"undo", "",
		"undo",
		"delete", "1", "y", "",
		"undo", "",
		"delete", "1", "y", "",
		"trash", "1", "", "",
		"undo", "", // undo the restore: back to trash
		"",
	}, "\n"))
	assertContains(t, out,
		"Nothing to undo.",
		"Undo the edit? expense 99.00 · 2026-10-17 14:33 goes back to expense 25,000.50 · 2026-10-17 14:33 [Y/n]: ",
		"Not undone.",
		"✓ Undone.",
		"Undo the delete of 1 entry (income 5,000,000.00)? [Y/n]: ",
		"Undo the restore of 1 entry (income 5,000,000.00)? They go back to trash. [Y/n]: ",
	)
	if strings.Count(out, "Nothing to undo.") != 2 {
		t.Error("second undo of the edit should say Nothing to undo")
	}
	rows := day(t, mem, 2026, 10, 17)
	if len(rows) != 1 || rows[0].Amount != 2_500_050 {
		t.Errorf("after undos: %+v", rows)
	}
	if trash, _ := mem.ListDeleted(context.Background()); len(trash) != 1 {
		t.Errorf("trash has %d, want 1 (restore undone)", len(trash))
	}
}

// changingStore edits a row behind the app's back right before the app saves.
type changingStore struct {
	*store.Memory
	sneak bool
}

func (c *changingStore) UpdateTransaction(ctx context.Context, id int64, v store.NewTransaction, expect time.Time) (store.Transaction, error) {
	if c.sneak {
		c.sneak = false
		other := v
		other.Amount = 12_345
		c.Memory.UpdateTransaction(ctx, id, other, expect)
	}
	return c.Memory.UpdateTransaction(ctx, id, v, expect)
}

func TestEditAndUndoAfterOutsideChange(t *testing.T) {
	cs := &changingStore{Memory: seeded(t), sneak: true}
	out := run(t, cs, "edit\n2 expense 10\n\n\n")
	assertContains(t, out, "✗ That entry was changed elsewhere since it was shown. Nothing was saved.")

	// The entry changes from "another window" right after the edit: undo must refuse.
	s2 := &outsideAfterEdit{Memory: seeded(t)}
	out = run(t, s2, "edit\n2 expense 10\n\n\nundo\n\n")
	assertContains(t, out, "✓ Updated.", "✗ That changed elsewhere since; undo skipped.")
	if rows := day(t, s2.Memory, 2026, 10, 17); rows[1].Amount != 777 {
		t.Errorf("undo overwrote the outside change: %+v", rows[1])
	}
}

// outsideAfterEdit changes the row again right after the app's edit succeeds.
type outsideAfterEdit struct {
	*store.Memory
	done bool
}

func (o *outsideAfterEdit) UpdateTransaction(ctx context.Context, id int64, v store.NewTransaction, expect time.Time) (store.Transaction, error) {
	tx, err := o.Memory.UpdateTransaction(ctx, id, v, expect)
	if err == nil && !o.done {
		o.done = true
		other := v
		other.Amount = 777
		o.Memory.UpdateTransaction(ctx, id, other, tx.UpdatedAt)
	}
	return tx, err
}

func TestParseDayInput(t *testing.T) {
	tests := []struct {
		in   string
		want dayInput
	}{
		{"", dayInput{action: actDone}},
		{" q ", dayInput{action: actDone}},
		{"p", dayInput{action: actPrev}},
		{"N", dayInput{action: actNext}},
		{"2026-10-15", dayInput{action: actDay, day: time.Date(2026, 10, 15, 0, 0, 0, 0, wib)}},
		{"2026-09", dayInput{action: actMonth, year: 2026, month: 9}},
		{"2026-9", dayInput{action: actMonth, year: 2026, month: 9}},
		{"2025", dayInput{action: actYear, year: 2025, month: 1}},
		{"2", dayInput{action: actPick, nums: []int{2}}},
		{"3 1 3", dayInput{action: actPick, nums: []int{3, 1}}},
		{"#2", dayInput{action: actPick, nums: []int{2}}},
		{"2 income 5000", dayInput{action: actEditLine, nums: []int{2}, edit: AddCmd{Kind: store.Income, Amount: 500_000}}},
		{"1 pendapatan 1 2026-10-16 08:00", dayInput{action: actEditLine, nums: []int{1},
			edit: AddCmd{Kind: store.Income, Amount: 100, At: time.Date(2026, 10, 16, 8, 0, 0, 0, wib)}}},
	}
	for _, tt := range tests {
		got, err := parseDayInput(tt.in, wib)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseDayInput(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"0", "-1", "x", "2 foo", "1969", "2026-00", "2026-10-32", "p n", "2 income"} {
		if got, err := parseDayInput(bad, wib); err == nil {
			t.Errorf("parseDayInput(%q) = %+v, want error", bad, got)
		}
	}
}

func TestParseEditDeleteCommands(t *testing.T) {
	d := time.Date(2026, 10, 15, 0, 0, 0, 0, wib)
	for in, want := range map[string]Command{
		"edit": EditCmd{}, "/edit 2026-10-15": EditCmd{Day: d}, "delete": DeleteCmd{}, "DELETE 2026-10-15": DeleteCmd{Day: d},
		"trash": TrashCmd{}, "undo": UndoCmd{},
	} {
		got, err := Parse(in, wib)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) = %#v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"delete 3", "edit 2026-10-15 09:00", "delete x y"} {
		if _, err := Parse(bad, wib); err == nil || !strings.Contains(err.Error(), "then choose the entry by its number") {
			t.Errorf("Parse(%q) error = %v", bad, err)
		}
	}
}
