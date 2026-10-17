package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"money-manager/internal/store"
	"money-manager/internal/voice"
)

func TestCategoriesOnAdd(t *testing.T) {
	mem := store.NewMemory()
	mem.AddCategory(context.Background(), store.Expense, "parkir")
	out := run(t, mem, strings.Join([]string{
		`expenses 25000 food "makan siang"`,
		"expenses 15000 Minuman",
		"income 5000000 salary 2026-10-01 09:00",
		"income 100 2026-10-02 10:00 GAJI",
		"expenses 2000 parkir",
		"expenses 9000",
		"expenses 1 none",
		"expenses 25000 fod",
		"income 5000 food",
		"expenses 25000 xyzzy",
		"expenses 25000 food makan siang",
		"expenses 25000 makan siang",
		"expenses 25000 2026-10-17 12:00 nasi goreng",
	}, "\n"))
	assertContains(t, out,
		"✓ Saved expense 25,000.00 · food · 2026-10-17 14:32 · makan siang",
		"✓ Saved expense 15,000.00 · drink · 2026-10-17 14:32\n",
		"✓ Saved income 5,000,000.00 · salary · 2026-10-01 09:00",
		"✓ Saved income 100.00 · salary · 2026-10-02 10:00",
		"✓ Saved expense 2,000.00 · parkir · 2026-10-17 14:32",
		"✓ Saved expense 9,000.00 · 2026-10-17 14:32\n",
		"✓ Saved expense 1.00 · 2026-10-17 14:32\n",
		`✗ Unknown expense category "fod". Did you mean food?`,
		"✗ food is an expense category. Income categories: salary",
		`✗ Unknown expense category "xyzzy". Type categories to see them all, or put a note in quotes: expenses 25000 "xyzzy"`,
		`✗ Put the note in quotes, e.g. expenses 25000 food "makan siang"`,
		`✗ Put the note in quotes, e.g. expenses 25000 "makan siang"`,
		`✗ Put the note in quotes, e.g. expenses 25000 2026-10-17 12:00 "nasi goreng"`,
	)
	if mem.Len() != 7 {
		t.Errorf("%d rows saved, want 7", mem.Len())
	}
}

func TestCategoryAddedElsewhereIsFound(t *testing.T) {
	// The session loads categories at start; one added afterwards (another window) is
	// found by reloading when the word is unknown.
	out := run(t, &addLater{Memory: store.NewMemory()}, "expenses 1\nexpenses 2000 bensin\n")
	assertContains(t, out, "✓ Saved expense 2,000.00 · bensin")
}

// addLater adds a category "from another window" on the first save.
type addLater struct {
	*store.Memory
	done bool
}

func (a *addLater) AddTransaction(ctx context.Context, v store.NewTransaction) (store.Transaction, error) {
	if !a.done {
		a.done = true
		a.Memory.AddCategory(ctx, store.Expense, "bensin")
	}
	return a.Memory.AddTransaction(ctx, v)
}

func TestListWithCategories(t *testing.T) {
	mem := store.NewMemory()
	out := run(t, mem, strings.Join([]string{
		`income 5000000 salary 2026-10-01 09:00 "gaji oktober"`,
		`expenses 150000 transport 2026-10-15 19:30 "bensin"`,
		`expenses 25000 food 2026-10-17 12:10 "makan siang"`,
		"expenses 15000 drink 2026-10-17 12:11",
		"expenses 9000 2026-10-17 12:12",
		"expenses 1000 2026-09-01 08:00",
		"income 2000 2026-09-02 08:00",
		"list 10 2026",
		"list 9 2026",
	}, "\n"))
	assertContains(t, out,
		"Type      Category          Amount   Note",
		"1   2026-10-01 09:00   income    salary      5,000,000.00   gaji oktober",
		"2   2026-10-15 19:30   expense   transport     150,000.00   bensin",
		"5   2026-10-17 12:12   expense                   9,000.00\n",
		"Expenses by category",
		"     transport     150,000.00    75%",
		"     food           25,000.00    13%",
		"     drink          15,000.00     8%",
		"     (none)          9,000.00     5%",
		"Income by category",
		"     salary      5,000,000.00   100%",
	)
	oct := out[strings.Index(out, "October 2026"):strings.Index(out, "September 2026")]
	if strings.Index(oct, "Expenses by category") > strings.Index(oct, "Income by category") {
		t.Error("expenses breakdown should come first")
	}
	sept := out[strings.Index(out, "September 2026"):]
	if strings.Contains(sept, "Category") || strings.Contains(sept, "by category") {
		t.Errorf("September has no categories but shows them:\n%s", sept)
	}
}

func TestCategoriesScreenAndManagement(t *testing.T) {
	mem := store.NewMemory()
	ctx := context.Background()
	mem.AddCategory(ctx, store.Income, "bonus")
	out := run(t, mem, strings.Join([]string{
		"category add expenses parkir",
		"category add expenses parkir", // again
		"category add expenses makanan",
		"category add income Bonus",
		"category add expenses bonus", // same name, other type: fine
		"category add expenses two-words-ok",
		"category add expenses 1abc",
		"expenses 2000 parkir",
		"expenses 3000 parkir",
		"categories",
		"category rename parkir parking",
		"category rename food meals",
		"category rename bonus thr", // both types have it
		"category rename income bonus thr",
		"category rename parkng x",
		"category remove gaji",
		"category remove two-words-ok", "y", // unused: just confirm
		"category remove parking", "parking", "salary", "xyz", "transport", "y",
		"category remove expenses bonus", "n",
		"categories",
	}, "\n"))
	assertContains(t, out,
		"✓ Added expense category parkir.",
		"✗ There is already an expense category parkir.",
		"✗ makanan already means food.",
		"✗ There is already an income category bonus.",
		"✓ Added expense category bonus.",
		"✓ Added expense category two-words-ok.",
		"✗ A category name is one word",
		"Expenses        entries\n    food                0   makanan\n",
		"    transport           0   transportasi, transpor, transpot",
		"    parkir              2   added by you",
		"Income          entries\n    salary              0   gaji\n    bonus               0   added by you",
		"✓ Renamed parkir to parking (2 entries).",
		"✗ food is built in and can't be renamed.",
		"✗ Both types have a category bonus. Say which: category rename expenses bonus thr",
		"✓ Renamed bonus to thr (0 entries).",
		`✗ No category "parkng". Did you mean parking?`,
		"✗ salary is built in and can't be removed.",
		"Remove two-words-ok? [y/N]: ",
		"✓ Removed two-words-ok.",
		"2 entries use parking. Move them to (category, or Enter for none): ",
		"✗ Choose another category than parking.",
		"✗ salary is an income category.",
		`✗ Unknown expense category "xyz". Choose one of: food, drink, dessert, laundry, subscription, transport, bonus, parking (or Enter for none).`,
		"Remove parking and move 2 entries to transport? [y/N]: ",
		"✓ Removed parking; 2 entries moved to transport.",
		"Remove bonus? [y/N]: ",
		"Nothing removed.",
		"    transport           2   transportasi",
	)
	rows := listAll(t, mem)
	if len(rows) != 2 || rows[0].Category != "transport" || rows[1].Category != "transport" {
		t.Errorf("entries after remove: %+v", rows)
	}
}

func TestRemoveCategoryToNone(t *testing.T) {
	mem := store.NewMemory()
	mem.AddCategory(context.Background(), store.Expense, "parkir")
	out := run(t, mem, "expenses 2000 parkir\ndelete\n1\ny\n\ncategory remove parkir\ny\n")
	// The only entry is in the trash: nothing to ask, it just loses the category.
	assertContains(t, out, "Remove parkir? [y/N]: ", "✓ Removed parkir; 1 entry now has no category.")
	trash, _ := mem.ListDeleted(context.Background())
	if len(trash) != 1 || trash[0].CategoryID != 0 {
		t.Errorf("trash = %+v", trash)
	}

	mem2 := store.NewMemory()
	mem2.AddCategory(context.Background(), store.Expense, "parkir")
	out = run(t, mem2, "expenses 2000 parkir\ncategory remove parkir\n\ny\n")
	assertContains(t, out, "Remove parkir? Its 1 entry will have no category. [y/N]: ", "✓ Removed parkir; 1 entry now has no category.")
}

func categorizedStore(t *testing.T) *store.Memory {
	t.Helper()
	mem := store.NewMemory()
	ctx := context.Background()
	cats, _ := mem.Categories(ctx)
	food, _ := store.FindCategory(cats, store.Expense, "food")
	salary, _ := store.FindCategory(cats, store.Income, "salary")
	at := func(hh, mm int) time.Time { return time.Date(2026, 10, 17, hh, mm, 0, 0, wib) }
	mem.AddTransactions(ctx, []store.NewTransaction{
		{Kind: store.Income, Amount: 500_000_000, OccurredAt: at(9, 0), CategoryID: salary.ID},
		{Kind: store.Expense, Amount: 2_500_000, OccurredAt: at(12, 10), CategoryID: food.ID, Note: "makan siang"},
		{Kind: store.Expense, Amount: 1_500_000, OccurredAt: at(12, 11)},
	})
	return mem
}

func TestCategoriesInDayViewAndEdit(t *testing.T) {
	mem := categorizedStore(t)
	out := run(t, mem, strings.Join([]string{
		"edit",
		"2", "", "minuman", "", "", "", "", // category by question (alias)
		"3", "", "parkir", "food", "", "", "", "", // unknown re-asks, then food
		"2", "", "-", "", "", "", "", // remove it
		"2", "income", "", "", "", "", "", // type change: question starts empty
		"3", "income", "", "", "", "", "", // food doesn't fit income: cleared
		"1", "", "", "", "", "", // Enter keeps salary: no changes
		"1 income 4000000", "", // shortcut without category keeps it
		"1 income 4000000 none", "", // shortcut removes it
		"2 expense 25000 drink", "", // shortcut sets it (and changes the type back)
		"2 expense 25000 salary", // wrong type in the shortcut
		"",
	}, "\n"))
	assertContains(t, out,
		"Time    Type      Category         Amount   Note",
		"1   09:00   income    salary     5,000,000.00\n",
		"2   12:10   expense   food          25,000.00   makan siang",
		"#2  expense 25,000.00 · food · 2026-10-17 12:10 · makan siang",
		"Category [food] (- to remove): ",
		"category     food  →  drink",
		"Category (Enter: none): ",
		`✗ Unknown expense category "parkir". Choose one of: food, drink, dessert, laundry, subscription, transport (or - for none).`,
		"category     (none)  →  food",
		"category     drink  →  (none)",
		"category     food  →  (none)",
		"Category [salary] (- to remove): ",
		"No changes.",
		"amount       5,000,000.00  →  4,000,000.00\n  Save?",
		"category     salary  →  (none)",
		"type         income  →  expense\n    category     (none)  →  drink",
		"✗ salary is an income category. Expense categories: food",
		"(or 2 expense 27500 food 2026-10-16 14:33)",
	)
	if strings.Contains(out, "category     salary  →  salary") {
		t.Error("kept category listed as a change")
	}
	rows := day(t, mem, 2026, 10, 17)
	if rows[0].Category != "" || rows[0].Amount != 400_000_000 || rows[1].Category != "drink" || rows[1].Kind != store.Expense ||
		rows[2].Category != "" || rows[2].Kind != store.Income {
		t.Errorf("after edits: %+v", rows)
	}
}

func TestCategoryUndoAndTrash(t *testing.T) {
	mem := categorizedStore(t)
	out := run(t, mem, strings.Join([]string{
		"edit", "2", "", "drink", "", "", "", "", "",
		"undo", "",
		"delete", "2", "y", "",
		"trash", "",
	}, "\n"))
	assertContains(t, out,
		"Undo the edit? expense 25,000.00 · drink · 2026-10-17 12:10 · makan siang goes back to expense 25,000.00 · food · 2026-10-17 12:10 · makan siang",
		"✓ Undone.",
		"Type      Category      Amount   Removed on",
	)
	trash, _ := mem.ListDeleted(context.Background())
	if len(trash) != 1 || trash[0].Category != "food" {
		t.Errorf("trash = %+v", trash)
	}
}

func TestCategoriesInVoiceReview(t *testing.T) {
	mem := store.NewMemory()
	mem.AddCategory(context.Background(), store.Expense, "parkir")
	script := speechScript(
		"pengeluaran dua puluh lima ribu makanan nasi padang",
		"pendapatan lima juta gaji oktober",
		"pengeluaran lima ribu parkir",
		"income seribu")
	run := runVoice(t, mem, [][]voice.Event{script}, strings.Join([]string{
		"/audio",
		"", // key: Enter → review
		"k 1", "drink",
		"k 4", "food", "salary", // wrong type re-asks
		"k 3", "-",
		"y",
		"",
	}, "\n"))
	assertContains(t, run.out,
		`✓ #1   expense      25,000.00 · food · 14:32   nasi padang   heard "pengeluaran dua puluh lima ribu makanan nasi padang"`,
		`✓ #2   income    5,000,000.00 · salary · 14:33   oktober`,
		`✓ #3   expense       5,000.00 · parkir · 14:34   heard`,
		`✓ #4   income        1,000.00 · 14:35   heard "income seribu"`,
		"Type      Category         Amount",
		"k <#>: category",
		"Category [food] (- to remove): ",
		"✗ food is an expense category. Income categories: salary",
		"✓ Saved 4 entries.",
	)
	rows := listAll(t, run.mem)
	got := []string{}
	for _, r := range rows {
		got = append(got, r.Category+"/"+r.Note)
	}
	if strings.Join(got, " ") != "drink/nasi padang salary/oktober / salary/" {
		t.Errorf("saved = %q", got)
	}
}

func TestPercent(t *testing.T) {
	tests := []struct {
		part, total int64
		want        int
	}{
		{150_000, 199_000, 75}, {25_000, 199_000, 13}, {9_000, 199_000, 5}, {1, 3, 33}, {1, 2, 50}, {2, 3, 67},
		{1, 1, 100}, {0, 5, 0}, {5, 0, 0},
		{99_999_999_999_999_999, 99_999_999_999_999_999, 100}, // would overflow part*100 in int64
	}
	for _, tt := range tests {
		if got := percent(tt.part, tt.total); got != tt.want {
			t.Errorf("percent(%d, %d) = %d, want %d", tt.part, tt.total, got, tt.want)
		}
	}
}
