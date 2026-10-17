package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"money-manager/internal/store"
	"money-manager/internal/voice"
)

func TestNotesAddAndList(t *testing.T) {
	mem := store.NewMemory()
	long := "oleh-oleh untuk keluarga di kampung halaman bulan ini"
	out := run(t, mem, strings.Join([]string{
		`income 5000000 2026-10-01 09:00 "gaji oktober"`,
		`expenses 25000 "Makan  Siang"`,
		`expenses 15000`,
		`expenses 99000 2026-10-02 10:00 "` + long + `"`,
		`expenses 1000 makan`,
		"list 10 2026",
		"list 9 2026",
		"income 1 2026-09-03 08:00",
		"list 9 2026",
	}, "\n"))
	assertContains(t, out,
		"✓ Saved income 5,000,000.00 · 2026-10-01 09:00 · gaji oktober",
		"✓ Saved expense 25,000.00 · 2026-10-17 14:32 · Makan Siang",
		"✓ Saved expense 15,000.00 · 2026-10-17 14:32\n",
		`✗ Unknown expense category "makan". Did you mean food? Type categories to see them all, or put a note in quotes: expenses 1000 "makan"`,
		"Amount   Note",
		"5,000,000.00   gaji oktober",
		"25,000.00   Makan Siang",
		"99,000.00   oleh-oleh untuk keluarga di k…", // 29 characters + "…"
	)
	if strings.Contains(out, long) && strings.Contains(out[strings.Index(out, "October 2026"):], long) {
		t.Error("long note not shortened in the list")
	}
	// A month without notes has no Note column.
	sept := out[strings.LastIndex(out, "September 2026"):]
	if strings.Contains(sept, "Note") {
		t.Errorf("Note column shown for a month without notes:\n%s", sept)
	}
	rows, _ := mem.ListBetween(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, wib), time.Date(2026, 11, 1, 0, 0, 0, 0, wib))
	if len(rows) != 4 || rows[1].Note != long {
		t.Errorf("stored notes: %+v", rows)
	}
}

func notedStore(t *testing.T) *store.Memory {
	t.Helper()
	mem := store.NewMemory()
	at := func(hh, mm int) time.Time { return time.Date(2026, 10, 17, hh, mm, 0, 0, wib) }
	mem.AddTransactions(context.Background(), []store.NewTransaction{
		{Kind: store.Income, Amount: 500_000_000, OccurredAt: at(9, 0), Note: "gaji oktober"},
		{Kind: store.Expense, Amount: 2_500_000, OccurredAt: at(12, 10), Note: "makan siang"},
		{Kind: store.Expense, Amount: 1_500_000, OccurredAt: at(12, 11)},
	})
	return mem
}

func TestNotesInDayViewAndEdit(t *testing.T) {
	mem := notedStore(t)
	out := run(t, mem, strings.Join([]string{
		"edit",
		"2", "", "", "", "", "makan siang kantor", "", // change the note by question
		"3", "", "", "", "", "bensin", "", // add a note where there was none
		"1", "", "", "", "", "-", "", // remove a note
		`2 expense 30000 "Makan Malam"`, "", // shortcut sets the note (case kept)
		"3 expense 16000", "", // shortcut without a note keeps it
		`3 expense 16000 ""`, "", // "" removes it
		"",
	}, "\n"))
	assertContains(t, out,
		"Amount   Note",
		"5,000,000.00   gaji oktober",
		"#2  expense 25,000.00 · 2026-10-17 12:10 · makan siang",
		"Note [makan siang] (- to remove): ",
		"note         makan siang  →  makan siang kantor",
		"Note (Enter: none): ",
		"note         (none)  →  bensin",
		"note         gaji oktober  →  (none)",
		"note         makan siang kantor  →  Makan Malam",
		"note         bensin  →  (none)",
	)
	if strings.Contains(out, "note         bensin  →  bensin") {
		t.Error("shortcut without a note listed a note change")
	}
	rows := day(t, mem, 2026, 10, 17)
	if rows[0].Note != "" || rows[1].Note != "Makan Malam" || rows[2].Note != "" || rows[2].Amount != 1_600_000 {
		t.Errorf("notes after edits: %q %q %q", rows[0].Note, rows[1].Note, rows[2].Note)
	}
}

func TestNotesUndoAndTrash(t *testing.T) {
	mem := notedStore(t)
	out := run(t, mem, strings.Join([]string{
		"edit", "2", "", "", "", "", "salah", "", "",
		"undo", "",
		"delete", "2", "y", "",
		"trash", "",
	}, "\n"))
	assertContains(t, out,
		"Undo the edit? expense 25,000.00 · 2026-10-17 12:10 · salah goes back to expense 25,000.00 · 2026-10-17 12:10 · makan siang",
		"#2  expense 25,000.00 · 2026-10-17 12:10 · makan siang",
		"Removed on   Note",
		"2026-11-",
		"makan siang",
	)
	trash, _ := mem.ListDeleted(context.Background())
	if len(trash) != 1 || trash[0].Note != "makan siang" {
		t.Errorf("trash = %+v (undo should have restored the note before the delete)", trash)
	}
}

func TestNotesInVoiceReview(t *testing.T) {
	script := speechScript("pengeluaran dua puluh lima ribu makan siang", "income seribu")
	run := runVoice(t, nil, [][]voice.Event{script}, strings.Join([]string{
		"/audio",
		"", // key: Enter → review
		"c 1", "makan siang kantor",
		"c 2", "uang kembalian",
		"c 1", "", // Enter keeps
		"c 3", // out of range
		"y",
		"",
	}, "\n"))
	assertContains(t, run.out,
		`✓ #1   expense      25,000.00 · 14:32   makan siang   heard "pengeluaran dua puluh lima ribu makan siang"`,
		`✓ #2   income        1,000.00 · 14:33   heard "income seribu"`,
		"Date & time        Note",
		"c <#>: note",
		"Note [makan siang] (- to remove): ",
		"Note (Enter: none): ",
		"makan siang kantor",
		"✗ Choose an entry number from 1 to 2",
		"✓ Saved 2 entries.",
	)
	rows := listAll(t, run.mem)
	if len(rows) != 2 || rows[0].Note != "makan siang kantor" || rows[1].Note != "uang kembalian" {
		t.Errorf("saved notes: %+v", rows)
	}
}

func TestShortNote(t *testing.T) {
	if got := shortNote("makan siang"); got != "makan siang" {
		t.Errorf("short note changed: %q", got)
	}
	long := strings.Repeat("é", 40)
	if got := shortNote(long); len([]rune(got)) != maxNoteCol || !strings.HasSuffix(got, "…") {
		t.Errorf("shortNote = %q", got)
	}
}
