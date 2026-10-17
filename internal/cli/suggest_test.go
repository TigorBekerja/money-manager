package cli

import (
	"context"
	"strings"
	"testing"

	"money-manager/internal/store"
)

func TestSuggestCommandNames(t *testing.T) {
	tests := []struct {
		in        string
		wantLines []string
	}{
		{"in", []string{"income "}},
		{"IN", []string{"income "}},
		{"e", []string{"expenses ", "edit", "exit"}},
		{"d", []string{"delete"}},
		{"u", []string{"undo"}},
		{"expense", []string{"expenses "}},
		{"l", []string{"list"}},
		{"he", []string{"help"}},
		{"/he", []string{"/help"}},
		{"t", []string{"trash", "tutorial"}},
		{"tu", []string{"tutorial"}},
		{"/tu", []string{"/tutorial"}},
		{"/", []string{"/income ", "/expenses ", "/list", "/edit", "/delete", "/trash", "/undo", "/categories", "/category ",
			"/audio", "/tutorial", "/audio-status", "/setup-audio", "/help", "/exit"}},
		{"cat", []string{"categories", "category "}},
		{"category", nil}, // complete; not a prefix of "categories"
		{"categories", nil},
		{"a", []string{"audio", "audio-status"}},
		{"audio", []string{"audio", "audio-status"}},
		{"/audio", []string{"/audio", "/audio-status"}},
		{"/se", []string{"/setup-audio"}},
		{"income", nil},
		{"x", nil},
		{"", nil},
		{" in", nil},
	}
	for _, tt := range tests {
		var got []string
		for _, it := range Suggest(tt.in).Items {
			got = append(got, it.Line)
		}
		if strings.Join(got, "|") != strings.Join(tt.wantLines, "|") {
			t.Errorf("Suggest(%q) lines = %q, want %q", tt.in, got, tt.wantLines)
		}
	}
}

func TestSuggestSubmitAndLabels(t *testing.T) {
	items := Suggest("/").Items
	submit := map[string]bool{}
	for _, it := range items {
		submit[it.Label] = it.Submit
	}
	want := map[string]bool{"income": false, "expenses": false, "list": true, "/audio": true, "/tutorial": true,
		"/audio-status": true, "/setup-audio": true, "/help": true, "/exit": true}
	for label, s := range want {
		if got, ok := submit[label]; !ok || got != s {
			t.Errorf("%s: Submit = %v (present %v), want %v", label, got, ok, s)
		}
	}
}

func TestSuggestArgumentHints(t *testing.T) {
	tests := []struct{ in, want string }{
		{"income", ` <amount> [category] [YYYY-MM-DD HH:MM] ["note"]`},
		{"income ", `<amount> [category] [YYYY-MM-DD HH:MM] ["note"]`},
		{"income 5000 ", `[category] [YYYY-MM-DD HH:MM] ["note"]`},
		{"income 5000 salary ", `[YYYY-MM-DD HH:MM] ["note"]`},
		{"income 5000 salary 2026-10-17 ", "HH:MM"},
		{"income 5000 salary 2026-10-17 09:00 ", `["note"]`},
		{"income 5000 2026-10-17 ", "HH:MM"},
		{"income 5000 2026-10-17 09:00 ", `["note"]`},
		{"income 5000 2026-10-17 09:00 salary ", `["note"]`},
		{"income 5000 salary x ", ""},
		{`expenses 25000 "makan `, ""}, // typing inside the quotes: no hint
		{`expenses 25000 "makan" `, ""},
		{"income 50", ""},
		{"expense ", `<amount> [category] [YYYY-MM-DD HH:MM] ["note"]`},
		{"/expenses ", `<amount> [category] [YYYY-MM-DD HH:MM] ["note"]`},
		{"category ", "add | rename | remove"},
		{"category add ", "<income|expenses> <name>"},
		{"category add expenses ", "<name>"},
		{"category add expenses parkir ", ""},
		{"category rename ", "[income|expenses] <name> <new name>"},
		{"category rename parkir ", "<new name>"},
		{"category rename expenses ", "<name> <new name>"},
		{"category rename expenses parkir ", "<new name>"},
		{"category remove ", "[income|expenses] <name>"},
		{"category remove expenses ", "<name>"},
		{"category remove parkir ", ""},
		{"categories ", ""},
		{"list ", "[<month> <year>]"},
		{"list 10 ", "<year>"},
		{"help ", ""},
	}
	for _, tt := range tests {
		if got := Suggest(tt.in).Hint; got != tt.want {
			t.Errorf("Suggest(%q).Hint = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSuggestCategories(t *testing.T) {
	cats, _ := store.NewMemory().Categories(context.Background())
	cats = append(cats, store.Category{ID: 99, Kind: store.Expense, Name: "parkir"})
	tests := []struct {
		in        string
		wantLines []string
	}{
		{"expenses 5000 d", []string{"expenses 5000 drink ", "expenses 5000 dessert "}}, // categories order
		{"expense  5000 F", []string{"expense  5000 food "}},                            // spacing kept, any case
		{"expenses 5000 p", []string{"expenses 5000 parkir "}},
		{"income 5000 s", []string{"income 5000 salary "}},
		{"income 5000 f", nil},           // food is for expenses
		{"expenses 5000 food", nil},      // typed in full: Enter saves
		{"expenses 5000 2026-10-1", nil}, // a date
		{"expenses 5000 food 2", nil},
		{"category re", []string{"category rename ", "category remove "}},
		{"category a", []string{"category add "}},
	}
	for _, tt := range tests {
		var got []string
		for _, it := range suggest(tt.in, cats).Items {
			got = append(got, it.Line)
		}
		if strings.Join(got, "|") != strings.Join(tt.wantLines, "|") {
			t.Errorf("suggest(%q) lines = %q, want %q", tt.in, got, tt.wantLines)
		}
	}
	items := suggest("expenses 5000 f", cats).Items
	if len(items) != 1 || items[0].Desc != "makanan" || items[0].Submit {
		t.Errorf("food item = %+v", items)
	}
	if items := suggest("expenses 5000 p", cats).Items; items[0].Desc != "your category" {
		t.Errorf("user category item = %+v", items)
	}
}

// Every suggested command line must parse (after adding arguments where needed).
func TestSuggestionsParse(t *testing.T) {
	for _, it := range Suggest("/").Items {
		line := it.Line
		switch {
		case it.Label == "category":
			line += "add expenses parkir"
		case !it.Submit:
			line += "100"
		}
		if _, err := Parse(line, wib); err != nil {
			t.Errorf("suggested %q does not parse: %v", line, err)
		}
	}
}
