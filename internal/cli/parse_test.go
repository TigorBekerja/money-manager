package cli

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"money-manager/internal/store"
)

var wib = time.FixedZone("WIB", 7*3600)

func TestParse(t *testing.T) {
	at := func(y int, m time.Month, d, hh, mm int) time.Time {
		return time.Date(y, m, d, hh, mm, 0, 0, wib)
	}
	tests := []struct {
		in   string
		want Command
	}{
		{"", nil},
		{"   ", nil},
		{"income 5000000", AddCmd{Kind: store.Income, Amount: 500_000_000}},
		{"INCOME 5,000,000", AddCmd{Kind: store.Income, Amount: 500_000_000}},
		{"/income 10", AddCmd{Kind: store.Income, Amount: 1000}},
		{"expenses 25000.50", AddCmd{Kind: store.Expense, Amount: 2_500_050}},
		{"expense 1", AddCmd{Kind: store.Expense, Amount: 100}},
		{"income 5000000 2026-10-17 09:00", AddCmd{Kind: store.Income, Amount: 500_000_000, At: at(2026, 10, 17, 9, 0)}},
		{"expenses 150000 2026-10-15 19:30", AddCmd{Kind: store.Expense, Amount: 15_000_000, At: at(2026, 10, 15, 19, 30)}},
		{"income 1 2026-10-17 9:05", AddCmd{Kind: store.Income, Amount: 100, At: at(2026, 10, 17, 9, 5)}},
		{"income 1 2028-02-29 00:00", AddCmd{Kind: store.Income, Amount: 100, At: at(2028, 2, 29, 0, 0)}},
		{"list", ListCmd{Prompt: true}},
		{"list 10 2026", ListCmd{Month: 10, Year: 2026}},
		{"/LIST 1 1970", ListCmd{Month: 1, Year: 1970}},
		{"tutorial", TutorialCmd{}},
		{"/tutorial", TutorialCmd{}},
		{"/audio", AudioCmd{}},
		{"/audio-status", AudioStatusCmd{}},
		{"/setup-audio", SetupAudioCmd{}},
		{"SETUP-AUDIO", SetupAudioCmd{}},
		{"/help", HelpCmd{}},
		{"help", HelpCmd{}},
		{"?", HelpCmd{}},
		{"/exit", ExitCmd{}},
		{"exit", ExitCmd{}},
		{"quit", ExitCmd{}},
		{"q", ExitCmd{}},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in, wib)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Parse(%q) = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		in      string
		wantErr string
	}{
		{"income", "Missing amount. Usage: income <amount> [YYYY-MM-DD HH:MM]   e.g. income 50000"},
		{"expenses", "Missing amount. Usage: expenses <amount>"},
		{"expenses -20", "Amount must be a positive number, e.g. 25000 or 25000.50"},
		{"income 0", "Amount must be a positive number"},
		{"income abc", "Amount must be a positive number"},
		{"income 1.234", "at most 2 decimals"},
		{"income 5000 2026-10-17", "Missing time. Use YYYY-MM-DD HH:MM, e.g. 2026-10-17 09:00"},
		{"income 5000 2026-02-30 09:00", `Invalid date "2026-02-30". Use YYYY-MM-DD HH:MM`},
		{"income 5000 2027-02-29 09:00", `Invalid date "2027-02-29"`},
		{"income 5000 17-10-2026 09:00", `Invalid date "17-10-2026"`},
		{"income 5000 1969-12-31 09:00", `Invalid date "1969-12-31"`},
		{"income 5000 2026-10-17 9pm", `Invalid time "9pm". Use 24-hour HH:MM, e.g. 21:00`},
		{"income 5000 2026-10-17 24:00", `Invalid time "24:00"`},
		{"income 5000 2026-10-17 09:60", `Invalid time "09:60"`},
		{"income 5000 2026-10-17 09:00 extra", "Too many arguments. Usage: income <amount>"},
		{"list 13 2026", "Month must be 1-12. Usage: list <month> <year>"},
		{"list 0 2026", "Month must be 1-12."},
		{"list 10 26", "Year must be 1970-9999."},
		{"list 10 1969", "Year must be 1970-9999."},
		{"list 10", "Usage: list <month> <year>   e.g. list 10 2026"},
		{"list 10 2026 x", "Usage: list <month> <year>"},
		{"spend 100", `Unknown command "spend". Type /help to see commands.`},
		{"/", `Unknown command "/".`},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in, wib)
		if err == nil {
			t.Errorf("Parse(%q) = %#v, want error containing %q", tt.in, got, tt.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("Parse(%q) error = %q, want it to contain %q", tt.in, err, tt.wantErr)
		}
	}
}
