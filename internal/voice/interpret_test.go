package voice

import (
	"errors"
	"strings"
	"testing"
	"time"

	"money-manager/internal/store"
)

var at = time.Date(2026, 10, 17, 14, 32, 0, 0, time.FixedZone("WIB", 7*3600))

func TestInterpret(t *testing.T) {
	const rp = 100 // cents per rupiah
	tests := []struct {
		text   string
		kind   store.Kind
		amount int64 // rupiah
	}{
		// Command words.
		{"income dua ratus ribu", store.Income, 200_000},
		{"Income, dua ratus ribu.", store.Income, 200_000},
		{"pendapatan dua ratus ribu", store.Income, 200_000},
		{"Pendapatan: satu juta", store.Income, 1_000_000},
		{"inkam dua ratus ribu", store.Income, 200_000},
		{"inkom lima ribu", store.Income, 5_000},
		{"in come lima ribu", store.Income, 5_000},
		{"expenses lima puluh ribu", store.Expense, 50_000},
		{"expense lima puluh ribu", store.Expense, 50_000},
		{"ekspenses lima puluh ribu", store.Expense, 50_000},
		{"pengeluaran lima puluh lima ribu", store.Expense, 55_000},
		{"pengeluran lima puluh lima ribu", store.Expense, 55_000},   // misspelt by the engine
		{"pencheliaran lima puluh lima ribu", store.Expense, 55_000}, // heard from an English accent
		{"pendapatn dua ribu", store.Income, 2_000},
		{"eh, pengeluaran dua puluh ribu", store.Expense, 20_000},
		{"oke income seribu", store.Income, 1_000},

		// Number words (AUDIO.md §4.2).
		{"income seratus dua puluh lima ribu", store.Income, 125_000},
		{"income sebelas ribu lima ratus", store.Income, 11_500},
		{"income dua juta lima ratus ribu", store.Income, 2_500_000},
		{"income satu setengah juta", store.Income, 1_500_000},
		{"income dua koma lima juta", store.Income, 2_500_000},
		{"income setengah juta", store.Income, 500_000},
		{"income sepuluh ribu", store.Income, 10_000},
		{"income dua belas ribu", store.Income, 12_000},
		{"income sembilan ratus sembilan puluh sembilan ribu sembilan ratus sembilan puluh sembilan", store.Income, 999_999},
		{"income seratus", store.Income, 100},
		{"income dua ratus lima", store.Income, 205},
		{"income seratus sepuluh ribu", store.Income, 110_000},
		{"income dua ratus dua belas ribu", store.Income, 212_000},
		{"income sejuta dua ratus ribu", store.Income, 1_200_000},
		{"income satu miliar", store.Income, 1_000_000_000},
		{"income dua milyar tiga ratus juta", store.Income, 2_300_000_000},
		{"income dua juta lima ratus", store.Income, 2_000_500},
		{"income dua ratus ribu rupiah", store.Income, 200_000},
		{"income dua ratus ribu buat gaji", store.Income, 200_000}, // trailing words ignored

		// Digits as the engine may write them.
		{"income 200.000", store.Income, 200_000},
		{"Income Rp200.000.", store.Income, 200_000},
		{"income Rp 200.000,00", store.Income, 200_000},
		{"income 1.500.000", store.Income, 1_500_000},
		{"income 200,000", store.Income, 200_000},
		{"income 200 ribu", store.Income, 200_000},
		{"income 2,5 juta", store.Income, 2_500_000},
		{"income 1.5 juta", store.Income, 1_500_000},
		{"pengeluaran 2 juta 500 ribu", store.Expense, 2_500_000},
		{"income 750", store.Income, 750},
	}
	for _, tt := range tests {
		d, err := Interpret(tt.text, at)
		if err != nil {
			t.Errorf("Interpret(%q) error: %v", tt.text, err)
			continue
		}
		if d.Kind != tt.kind || d.Amount != tt.amount*rp {
			t.Errorf("Interpret(%q) = %s %d, want %s %d", tt.text, d.Kind, d.Amount/rp, tt.kind, tt.amount)
		}
		if !d.At.Equal(at) || !d.Recorded.Equal(at) || d.Heard != strings.TrimSpace(tt.text) {
			t.Errorf("Interpret(%q) metadata = %+v", tt.text, d)
		}
	}
}

func TestInterpretDecimals(t *testing.T) {
	d, err := Interpret("income dua koma lima", at)
	if err != nil || d.Amount != 250 {
		t.Errorf("dua koma lima = %d cents, %v; want 250", d.Amount, err)
	}
	d, err = Interpret("income 1.234,5", at)
	if err != nil || d.Amount != 123_450 {
		t.Errorf("1.234,5 = %d cents, %v", d.Amount, err)
	}
}

func TestInterpretRejects(t *testing.T) {
	tests := []struct{ text, wantErr string }{
		{"beli kopi", "no income/expenses word"},
		{"dua ratus ribu", "no income/expenses word"},
		{"belanja dua ratus ribu", "no income/expenses word"}, // only pendapatan/pengeluaran
		{"pemasukan dua ratus ribu", "no income/expenses word"},
		{"income", "no amount heard after"},
		{"income buat makan", "no amount heard"},
		{"income goceng", "no amount heard"}, // no slang
		{"income cepek", "no amount heard"},
		{"income dua ratus rebu", `"rebu" isn't a number word`}, // no colloquial "rebu"
		{"income lima juto", `"juto" isn't a number word`},
		{"income nol", "more than zero"},
		{"income ribu", "needs a number before it"},
		{"income dua ribu tiga ribu", "couldn't understand the amount"},
		{"income dua lima", "couldn't understand the amount"},
		{"income dua koma", "no digits after"},
		{"income 12.34.56", "couldn't understand the number"},
		{"income 9999999999999999", "too large"},
		{"income dua koma satu dua tiga", "too many decimals"},
	}
	for _, tt := range tests {
		d, err := Interpret(tt.text, at)
		if err == nil {
			t.Errorf("Interpret(%q) = %+v, want error %q", tt.text, d, tt.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("Interpret(%q) error = %q, want %q", tt.text, err, tt.wantErr)
		}
	}
}

func TestIsStop(t *testing.T) {
	for _, s := range []string{"stop", "Stop.", "STOP!", "berhenti", "Berhenti.", "setop", "berenti", "berhentii", "stop stop", "oke, stop", "eh berhenti"} {
		if !IsStop(s) {
			t.Errorf("IsStop(%q) = false", s)
		}
	}
	for _, s := range []string{"", "income stop", "stop dua ratus ribu", "pengeluaran berhenti", "shop", "jangan berhenti dulu ya", "[BLANK_AUDIO]"} {
		if IsStop(s) {
			t.Errorf("IsStop(%q) = true", s)
		}
	}
}

func TestInterpretNothingHeard(t *testing.T) {
	for _, text := range []string{"", "   ", "[BLANK_AUDIO]", "(musik)", "Terima kasih.", "eh", "..."} {
		if _, err := Interpret(text, at); !errors.Is(err, ErrNothingHeard) {
			t.Errorf("Interpret(%q) error = %v, want ErrNothingHeard", text, err)
		}
	}
}
