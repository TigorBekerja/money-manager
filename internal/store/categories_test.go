package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func categoryByName(t *testing.T, s backend, kind Kind, name string) Category {
	t.Helper()
	cats, err := s.Categories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c, ok := FindCategory(cats, kind, name)
	if !ok {
		t.Fatalf("no %s category %q in %+v", kind, name, cats)
	}
	return c
}

func names(cats []Category) string {
	var out []string
	for _, c := range cats {
		out = append(out, string(c.Kind)+":"+c.Name)
	}
	return strings.Join(out, " ")
}

func testCategories(t *testing.T, newBackend func(t *testing.T) backend) {
	ctx := context.Background()
	at := time.Date(2026, 10, 17, 12, 10, 0, 0, wib)
	oct := time.Date(2026, 10, 1, 0, 0, 0, 0, wib)
	nov := oct.AddDate(0, 1, 0)

	t.Run("built-in categories are there", func(t *testing.T) {
		s := newBackend(t)
		cats, err := s.Categories(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := "expense:food expense:drink expense:dessert expense:laundry expense:subscription expense:transport income:salary"
		if got := names(cats); got != want {
			t.Errorf("categories = %s\nwant %s", got, want)
		}
		for _, c := range cats {
			if !c.Builtin || c.ID == 0 {
				t.Errorf("%+v: want a built-in with an id", c)
			}
		}
		if c, ok := FindCategory(cats, Expense, "Makanan"); !ok || c.Name != "food" {
			t.Errorf("alias makanan = %+v, %v", c, ok)
		}
		if _, ok := FindCategory(cats, Income, "food"); ok {
			t.Error("food found as an income category")
		}
	})

	t.Run("entries carry a category of their kind", func(t *testing.T) {
		s := newBackend(t)
		food := categoryByName(t, s, Expense, "food")
		drink := categoryByName(t, s, Expense, "drink")
		salary := categoryByName(t, s, Income, "salary")

		tx, err := s.AddTransaction(ctx, NewTransaction{Kind: Expense, Amount: 2_500_000, OccurredAt: at, CategoryID: food.ID})
		if err != nil || tx.CategoryID != food.ID || tx.Category != "food" {
			t.Fatalf("add = %+v, %v", tx, err)
		}
		plain, _ := s.AddTransaction(ctx, NewTransaction{Kind: Expense, Amount: 100, OccurredAt: at})
		if plain.CategoryID != 0 || plain.Category != "" {
			t.Errorf("entry without category = %+v", plain)
		}
		if _, err := s.AddTransaction(ctx, NewTransaction{Kind: Income, Amount: 1, OccurredAt: at, CategoryID: food.ID}); !errors.Is(err, ErrCategoryKind) {
			t.Errorf("expense category on income: err = %v", err)
		}
		if _, err := s.AddTransaction(ctx, NewTransaction{Kind: Expense, Amount: 1, OccurredAt: at, CategoryID: 99999}); !errors.Is(err, ErrCategoryKind) {
			t.Errorf("missing category: err = %v", err)
		}
		if _, err := s.AddTransactions(ctx, []NewTransaction{
			{Kind: Income, Amount: 1, OccurredAt: at, CategoryID: salary.ID},
			{Kind: Income, Amount: 2, OccurredAt: at, CategoryID: drink.ID},
		}); !errors.Is(err, ErrCategoryKind) {
			t.Errorf("batch with a wrong category: err = %v", err)
		}

		up, err := s.UpdateTransaction(ctx, tx.ID, NewTransaction{Kind: Expense, Amount: tx.Amount, OccurredAt: at, CategoryID: drink.ID}, tx.UpdatedAt)
		if err != nil || up.Category != "drink" {
			t.Fatalf("update = %+v, %v", up, err)
		}
		if _, err := s.UpdateTransaction(ctx, tx.ID, NewTransaction{Kind: Income, Amount: tx.Amount, OccurredAt: at, CategoryID: drink.ID}, up.UpdatedAt); !errors.Is(err, ErrCategoryKind) {
			t.Errorf("kind changed but category kept: err = %v", err)
		}
		// Undo restores the old values, category included.
		back, err := s.UpdateTransaction(ctx, tx.ID, tx.Values(), up.UpdatedAt)
		if err != nil || back.Category != "food" {
			t.Errorf("undo = %+v, %v", back, err)
		}

		s.DeleteTransactions(ctx, []int64{tx.ID})
		if trash, _ := s.ListDeleted(ctx); len(trash) != 1 || trash[0].Category != "food" {
			t.Errorf("trash = %+v", trash)
		}
		if restored, _ := s.RestoreTransactions(ctx, []int64{tx.ID}); len(restored) != 1 || restored[0].Category != "food" {
			t.Errorf("restored = %+v", restored)
		}
		got, _ := s.ListBetween(ctx, oct, nov)
		if len(got) != 2 || got[0].Category != "food" || got[1].Category != "" {
			t.Errorf("list = %+v", got)
		}
	})

	t.Run("add, rename and remove user categories", func(t *testing.T) {
		s := newBackend(t)
		parkir, err := s.AddCategory(ctx, Expense, "parkir")
		if err != nil || parkir.ID == 0 || parkir.Builtin || parkir.Name != "parkir" {
			t.Fatalf("add = %+v, %v", parkir, err)
		}
		if _, err := s.AddCategory(ctx, Expense, "bensin"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddCategory(ctx, Income, "parkir"); err != nil {
			t.Errorf("same name for the other kind: %v", err)
		}
		cats, _ := s.Categories(ctx)
		want := "expense:food expense:drink expense:dessert expense:laundry expense:subscription expense:transport " +
			"expense:bensin expense:parkir income:salary income:parkir"
		if got := names(cats); got != want {
			t.Errorf("order = %s\nwant %s", got, want)
		}
		for _, name := range []string{"parkir", "food", "makanan"} {
			if _, err := s.AddCategory(ctx, Expense, name); !errors.Is(err, ErrCategoryExists) {
				t.Errorf("add %q again: err = %v", name, err)
			}
		}
		for _, name := range []string{"none", "Parkir", "two words", "1abc", "", strings.Repeat("x", MaxCategoryLen+1)} {
			if _, err := s.AddCategory(ctx, Expense, name); err == nil {
				t.Errorf("name %q accepted", name)
			}
		}
		if _, err := s.AddCategory(ctx, Expense, strings.Repeat("x", MaxCategoryLen)); err != nil {
			t.Errorf("20-character name refused: %v", err)
		}

		tx, _ := s.AddTransaction(ctx, NewTransaction{Kind: Expense, Amount: 500_000, OccurredAt: at, CategoryID: parkir.ID})
		trashed, _ := s.AddTransaction(ctx, NewTransaction{Kind: Expense, Amount: 200_000, OccurredAt: at, CategoryID: parkir.ID})
		s.DeleteTransactions(ctx, []int64{trashed.ID})
		counts, err := s.CategoryCounts(ctx)
		if err != nil || counts[parkir.ID] != 1 {
			t.Errorf("counts = %v, %v (the trashed entry doesn't count)", counts, err)
		}

		renamed, err := s.RenameCategory(ctx, parkir.ID, "parking")
		if err != nil || renamed.Name != "parking" {
			t.Fatalf("rename = %+v, %v", renamed, err)
		}
		if got, _ := s.ListBetween(ctx, oct, nov); len(got) != 1 || got[0].Category != "parking" {
			t.Errorf("entry after rename = %+v", got)
		}
		if _, err := s.RenameCategory(ctx, parkir.ID, "bensin"); !errors.Is(err, ErrCategoryExists) {
			t.Errorf("rename onto another category: err = %v", err)
		}
		if _, err := s.RenameCategory(ctx, parkir.ID, "parking"); err != nil {
			t.Errorf("rename to its own name: %v", err)
		}
		food := categoryByName(t, s, Expense, "food")
		if _, err := s.RenameCategory(ctx, food.ID, "meals"); !errors.Is(err, ErrBuiltin) {
			t.Errorf("rename built-in: err = %v", err)
		}
		if _, err := s.RenameCategory(ctx, 99999, "x"); !errors.Is(err, ErrNotFound) {
			t.Errorf("rename missing: err = %v", err)
		}

		transport := categoryByName(t, s, Expense, "transport")
		salary := categoryByName(t, s, Income, "salary")
		if _, err := s.RemoveCategory(ctx, food.ID, 0); !errors.Is(err, ErrBuiltin) {
			t.Errorf("remove built-in: err = %v", err)
		}
		if _, err := s.RemoveCategory(ctx, parkir.ID, salary.ID); !errors.Is(err, ErrCategoryKind) {
			t.Errorf("move to an income category: err = %v", err)
		}
		if _, err := s.RemoveCategory(ctx, parkir.ID, parkir.ID); !errors.Is(err, ErrCategoryKind) {
			t.Errorf("move to itself: err = %v", err)
		}
		moved, err := s.RemoveCategory(ctx, parkir.ID, transport.ID)
		if err != nil || moved != 2 {
			t.Fatalf("remove = %d, %v; want 2 moved (one in the trash)", moved, err)
		}
		if got, _ := s.ListBetween(ctx, oct, nov); len(got) != 1 || got[0].Category != "transport" {
			t.Errorf("entry after remove = %+v", got)
		}
		if trash, _ := s.ListDeleted(ctx); len(trash) != 1 || trash[0].Category != "transport" {
			t.Errorf("trash after remove = %+v", trash)
		}
		// Moving is a change to the entry: an edit based on the old version is refused.
		if _, err := s.UpdateTransaction(ctx, tx.ID, tx.Values(), tx.UpdatedAt); !errors.Is(err, ErrChanged) {
			t.Errorf("edit after the move: err = %v", err)
		}
		if _, err := s.RemoveCategory(ctx, parkir.ID, 0); !errors.Is(err, ErrNotFound) {
			t.Errorf("remove twice: err = %v", err)
		}

		bensin := categoryByName(t, s, Expense, "bensin")
		s.AddTransaction(ctx, NewTransaction{Kind: Expense, Amount: 1, OccurredAt: at, CategoryID: bensin.ID})
		if moved, err := s.RemoveCategory(ctx, bensin.ID, 0); err != nil || moved != 1 {
			t.Errorf("remove to none = %d, %v", moved, err)
		}
		got, _ := s.ListBetween(ctx, oct, nov)
		if len(got) != 2 || got[1].CategoryID != 0 || got[1].Category != "" {
			t.Errorf("entries after remove to none = %+v", got)
		}
	})
}

func TestCheckCategoryName(t *testing.T) {
	cats := NewMemory().cats
	tests := []struct {
		kind Kind
		name string
		want string // "" = valid
	}{
		{Expense, "parkir", ""},
		{Expense, "uang-sekolah", ""},
		{Income, "food", ""}, // food is only an expense category
		{Expense, "food", "There is already an expense category food."},
		{Expense, "makanan", "makanan already means food."},
		{Expense, "pencuci-mulut", "pencuci-mulut already means dessert."},
		{Income, "gaji", "gaji already means salary."},
		{Expense, "none", `"none" means no category; choose another name.`},
		{Expense, "two words", "A category name is one word"},
		{Expense, strings.Repeat("a", 21), "at most 20 characters"},
	}
	for _, tt := range tests {
		err := CheckCategoryName(cats, tt.kind, tt.name, 0)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s %q: %v", tt.kind, tt.name, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s %q: err = %v, want %q", tt.kind, tt.name, err, tt.want)
		}
	}
}
