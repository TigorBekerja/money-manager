package cli

import (
	"strings"
	"testing"
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
		{"/", []string{"/income ", "/expenses ", "/list", "/edit", "/delete", "/trash", "/undo", "/audio", "/tutorial", "/audio-status", "/setup-audio", "/help", "/exit"}},
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
		{"income", " <amount> [YYYY-MM-DD HH:MM]"},
		{"income ", "<amount> [YYYY-MM-DD HH:MM]"},
		{"income 5000 ", "[YYYY-MM-DD HH:MM]"},
		{"income 5000 2026-10-17 ", "HH:MM"},
		{"income 5000 2026-10-17 09:00 ", ""},
		{"income 50", ""},
		{"expense ", "<amount> [YYYY-MM-DD HH:MM]"},
		{"/expenses ", "<amount> [YYYY-MM-DD HH:MM]"},
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

// Every suggested command line must parse (after adding an amount where needed).
func TestSuggestionsParse(t *testing.T) {
	for _, it := range Suggest("/").Items {
		line := it.Line
		if !it.Submit {
			line += "100"
		}
		if _, err := Parse(line, wib); err != nil {
			t.Errorf("suggested %q does not parse: %v", line, err)
		}
	}
}
