package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"money-manager/internal/store"
)

var fixedNow = time.Date(2026, 10, 17, 14, 32, 45, 0, wib)

func run(t *testing.T, st Store, input string) string {
	t.Helper()
	var out bytes.Buffer
	r := &REPL{
		In:     strings.NewReader(input),
		Out:    &out,
		Store:  st,
		Now:    func() time.Time { return fixedNow },
		Banner: BannerInfo{Version: "0.1.0", Name: "Vandika", DB: "localhost:5432/money_manager"},
	}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String()
}

func assertContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n--- output ---\n%s", w, out)
		}
	}
}

func TestBanner(t *testing.T) {
	out := run(t, store.NewMemory(), "/exit\n")
	assertContains(t, out,
		"Money Manager", "v0.1.0",
		"Hi Vandika! Type /help to see commands.",
		"New here? Type /tutorial for a quick guide.",
		"db: localhost:5432/money_manager",
		"Bye!")
}

func TestAddAndList(t *testing.T) {
	mem := store.NewMemory()
	out := run(t, mem, strings.Join([]string{
		"income 5000000 2026-10-01 09:00",
		"expenses 150000 2026-10-15 19:30",
		"expenses 25000.50",
		"income 999 2026-11-01 00:00",
		"list 10 2026",
		"exit",
	}, "\n"))

	assertContains(t, out,
		"✓ Saved income 5,000,000.00 · 2026-10-01 09:00",
		"✓ Saved expense 150,000.00 · 2026-10-15 19:30",
		"✓ Saved expense 25,000.50 · 2026-10-17 14:32", // now, truncated to the minute
		"October 2026",
		"1   2026-10-01 09:00   income    5,000,000.00",
		"2   2026-10-15 19:30   expense     150,000.00",
		"3   2026-10-17 14:32   expense      25,000.50",
	)
	if strings.Contains(out, "(practice") {
		t.Error("real session output should not be tagged as practice")
	}
	if strings.Contains(out[strings.Index(out, "October 2026"):], "2026-11-01") {
		t.Error("November entry leaked into October list")
	}
	if mem.Len() != 4 {
		t.Errorf("store has %d rows, want 4", mem.Len())
	}

	totals := out[strings.Index(out, "October 2026"):]
	assertContains(t, totals, "Income", "5,000,000.00", "Expenses", "175,000.50", "Net", "4,824,999.50")
}

func TestTotalsAlignedAndNegativeNet(t *testing.T) {
	out := run(t, store.NewMemory(), "income 10 2026-10-02 08:00\nexpenses 1500 2026-10-03 08:00\nlist 10 2026\n")
	assertContains(t, out, "-1,490.00")

	var amountCols []int
	for _, l := range strings.Split(out, "\n") {
		for _, v := range []string{"10.00", "1,500.00", "-1,490.00"} {
			if strings.HasSuffix(l, " "+v) {
				amountCols = append(amountCols, len(l))
			}
		}
	}
	if len(amountCols) < 4 {
		t.Fatalf("expected rows and totals, got %v\n%s", amountCols, out)
	}
	for _, c := range amountCols {
		if c != amountCols[0] {
			t.Errorf("amount column not right-aligned: line lengths %v\n%s", amountCols, out)
			break
		}
	}
}

func TestListPrompt(t *testing.T) {
	out := run(t, store.NewMemory(), "list\n13\n9\nabc\n2025\nexit\n")
	assertContains(t, out,
		"Month (1-12) [10]: ",
		"Year [2026]: ",
		"✗ Month must be 1-12.",
		"✗ Year must be 1970-9999.",
		"September 2025",
		"No transactions.")
}

func TestListPromptDefaultsToCurrentMonth(t *testing.T) {
	out := run(t, store.NewMemory(), "income 100\nincome 7 2026-09-30 23:59\nlist\n\n\nlist\n\n2025\nexit\n")
	month := out[strings.Index(out, "October 2026"):]
	assertContains(t, month, "100.00")
	if strings.Contains(month[:strings.Index(month, "Net")], "2026-09-30") {
		t.Error("September entry shown in the default (October) list")
	}
	// Enter on month only keeps the current month with the typed year.
	assertContains(t, out, "October 2025")
}

func TestErrorsKeepSessionRunning(t *testing.T) {
	mem := store.NewMemory()
	out := run(t, mem, "income\nexpenses -20\nincome 5000 2026-02-30 09:00\nspend 100\nincome 1\nexit\n")
	assertContains(t, out,
		"✗ Missing amount.",
		"✗ Amount must be a positive number",
		`✗ Invalid date "2026-02-30"`,
		`✗ Unknown command "spend". Type /help to see commands.`,
		"✓ Saved income 1.00")
	if mem.Len() != 1 {
		t.Errorf("store has %d rows, want 1", mem.Len())
	}
}

func TestLeadingBOMIgnored(t *testing.T) {
	mem := store.NewMemory()
	out := run(t, mem, "\xef\xbb\xbf/tutorial\nq\n")
	assertContains(t, out, "Tutorial · 1/7")
	if strings.Contains(out, "Unknown command") {
		t.Errorf("BOM not stripped:\n%s", out)
	}
}

func TestHelp(t *testing.T) {
	out := run(t, store.NewMemory(), "/help\n")
	assertContains(t, out, "income <amount> [YYYY-MM-DD HH:MM]", "/tutorial", "/exit")
}

func TestEOFExits(t *testing.T) {
	out := run(t, store.NewMemory(), "income 1")
	assertContains(t, out, "✓ Saved income 1.00", "Bye!")
}

func TestColorOff(t *testing.T) {
	out := run(t, store.NewMemory(), "income 1\nlist 10 2026\n")
	if strings.Contains(out, "\x1b[") {
		t.Error("ANSI codes in output with Color=false")
	}
}
