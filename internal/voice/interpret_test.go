package voice

import (
	"context"
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

func TestInterpretNote(t *testing.T) {
	tests := []struct {
		text   string
		amount int64 // rupiah
		note   string
	}{
		{"pengeluaran dua puluh lima ribu makan siang", 25_000, "makan siang"},
		{"Pengeluaran dua puluh lima ribu, makan siang.", 25_000, "makan siang"},
		{"pengeluaran lima puluh ribu buat makan", 50_000, "buat makan"}, // kept as said
		{"pendapatan lima juta gaji oktober", 5_000_000, "gaji oktober"},
		{"pengeluaran dua puluh lima ribu rupiah makan", 25_000, "makan"}, // rupiah belongs to the amount
		{"pengeluaran 25.000 makan siang", 25_000, "makan siang"},
		{"expenses Rp 25.000 bensin", 25_000, "bensin"},
		{"pengeluaran lima puluh ribu beli kopi seribu", 50_000, "beli kopi seribu"}, // number words inside the note
		{"pengeluaran lima puluh ribu beli dua kopi", 50_000, "beli dua kopi"},
		{"income dua ratus ribu", 200_000, ""},
		{"income dua ratus ribu rupiah", 200_000, ""},
	}
	for _, tt := range tests {
		d, err := Interpret(tt.text, at)
		if err != nil {
			t.Errorf("Interpret(%q) error: %v", tt.text, err)
			continue
		}
		if d.Amount != tt.amount*100 || d.Note != tt.note {
			t.Errorf("Interpret(%q) = %d %q, want %d %q", tt.text, d.Amount/100, d.Note, tt.amount, tt.note)
		}
	}

	long := "income seribu " + strings.Repeat("kata ", 60)
	if d, err := Interpret(long, at); err != nil || len([]rune(d.Note)) != 200 {
		t.Errorf("long note: %d characters, %v; want cut to 200", len([]rune(d.Note)), err)
	}
	// A misheard scale word right after the amount still rejects the entry.
	if _, err := Interpret("pengeluaran dua ratus rebu makan", at); err == nil {
		t.Error("dua ratus rebu makan accepted")
	}
}

func TestInterpretCategory(t *testing.T) {
	mem := store.NewMemory()
	mem.AddCategory(context.Background(), store.Expense, "parkir")
	mem.AddCategory(context.Background(), store.Expense, "uang-sekolah")
	cats, _ := mem.Categories(context.Background())
	in := Interpreter{Categories: cats}
	tests := []struct {
		text     string
		amount   int64 // rupiah
		category string
		note     string
	}{
		{"pengeluaran lima puluh ribu makanan nasi padang", 50_000, "food", "nasi padang"}, // taken out of the note
		{"pengeluaran lima puluh ribu food", 50_000, "food", ""},
		{"pengeluaran dua puluh ribu minuman es teh", 20_000, "drink", "es teh"},
		{"pengeluaran dua puluh ribu rupiah minuman", 20_000, "drink", ""},
		{"pendapatan lima juta gaji oktober", 5_000_000, "salary", "oktober"},
		{"pengeluaran lima belas ribu pencuci mulut es krim", 15_000, "dessert", "es krim"}, // two-word alias
		{"pengeluaran lima belas ribu makanan penutup puding", 15_000, "dessert", "puding"}, // longest match wins over food
		{"pengeluaran lima puluh ribu langanan netflix", 50_000, "subscription", "netflix"}, // one letter off, 6+ letters
		{"pengeluaran tiga puluh ribu transportasi ojek", 30_000, "transport", "ojek"},
		{"pengeluaran lima ribu parkir", 5_000, "parkir", ""}, // the user's own
		{"pengeluaran satu juta uang sekolah", 1_000_000, "uang-sekolah", ""},
		{"pengeluaran lima puluh ribu buat makan", 50_000, "", "buat makan"},
		{"pengeluaran lima puluh ribu makan siang", 50_000, "", "makan siang"}, // "makan" isn't "makanan"
		{"pengeluaran lima ribu gaji supir", 5_000, "", "gaji supir"},          // salary is for income
		{"pendapatan lima ribu makanan", 5_000, "", "makanan"},                 // food is for expenses
		{"pengeluaran lima ribu beli makanan", 5_000, "", "beli makanan"},      // only right after the amount
		{"pengeluaran lima ribu parkr", 5_000, "parkir", ""},                   // one letter off, 6+ letters
		{"pengeluaran lima ribu fod", 5_000, "", "fod"},                        // short words must be exact
		{"pengeluaran lima ribu gajih", 5_000, "", "gajih"},
	}
	for _, tt := range tests {
		d, err := in.Interpret(tt.text, at)
		if err != nil {
			t.Errorf("Interpret(%q) error: %v", tt.text, err)
			continue
		}
		if d.Amount != tt.amount*100 || d.Category != tt.category || d.Note != tt.note {
			t.Errorf("Interpret(%q) = %d %q %q, want %d %q %q", tt.text, d.Amount/100, d.Category, d.Note, tt.amount, tt.category, tt.note)
		}
		if (d.CategoryID != 0) != (tt.category != "") {
			t.Errorf("Interpret(%q): CategoryID = %d", tt.text, d.CategoryID)
		}
	}
	// The misheard-number guard still applies when no category follows the amount.
	if _, err := in.Interpret("pengeluaran dua ratus rebu makanan", at); err == nil {
		t.Error("dua ratus rebu makanan accepted")
	}
	// Without categories (the package-level Interpret) the words stay in the note.
	if d, _ := Interpret("pengeluaran lima puluh ribu makanan nasi padang", at); d.Category != "" || d.Note != "makanan nasi padang" {
		t.Errorf("without categories: %+v", d)
	}
}

func TestSplit(t *testing.T) {
	tests := []struct {
		text    string
		entries []string
		stop    bool
	}{
		{"Pengeluaran lima puluh ribu, makan siang.", []string{"Pengeluaran lima puluh ribu, makan siang."}, false}, // as is
		{"stop", nil, true},
		{"Oke, berhenti.", nil, true},
		{"pengeluaran lima puluh ribu makan siang. Stop.", []string{"pengeluaran lima puluh ribu makan siang"}, true},
		{"income seribu berhenti oke", []string{"income seribu"}, true},
		{"income seribu stop dulu", []string{"income seribu stop dulu"}, false}, // stop must be last
		{"pengeluaran lima puluh ribu makan siang pendapatan dua juta gaji",
			[]string{"pengeluaran lima puluh ribu makan siang", "pendapatan dua juta gaji"}, false},
		{"income seribu, expenses 25.000 bensin, pengeluran dua ribu parkir. Stop.",
			[]string{"income seribu", "expenses 25.000 bensin", "pengeluran dua ribu parkir"}, true},
		{"pengeluaran lima puluh ribu biaya pengeluaran kantor", // no amount after the second: a note
			[]string{"pengeluaran lima puluh ribu biaya pengeluaran kantor"}, false},
		{"beli kopi stop", []string{"beli kopi"}, true},
	}
	for _, tt := range tests {
		entries, stop := Split(tt.text)
		if strings.Join(entries, "|") != strings.Join(tt.entries, "|") || stop != tt.stop {
			t.Errorf("Split(%q) = %q, %v; want %q, %v", tt.text, entries, stop, tt.entries, tt.stop)
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
