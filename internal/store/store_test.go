package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type backend interface {
	AddTransaction(ctx context.Context, v NewTransaction) (Transaction, error)
	AddTransactions(ctx context.Context, rows []NewTransaction) ([]Transaction, error)
	Nearest(ctx context.Context, t time.Time, before bool) (time.Time, bool, error)
	UpdateTransaction(ctx context.Context, id int64, v NewTransaction, expectUpdatedAt time.Time) (Transaction, error)
	DeleteTransactions(ctx context.Context, ids []int64) ([]Transaction, error)
	RestoreTransactions(ctx context.Context, ids []int64) ([]Transaction, error)
	ListDeleted(ctx context.Context) ([]Transaction, error)
	PurgeDeleted(ctx context.Context, before time.Time) (int, error)
	ListBetween(ctx context.Context, from, to time.Time) ([]Transaction, error)
	Categories(ctx context.Context) ([]Category, error)
	AddCategory(ctx context.Context, kind Kind, name string) (Category, error)
	RenameCategory(ctx context.Context, id int64, name string) (Category, error)
	RemoveCategory(ctx context.Context, id, moveTo int64) (int, error)
	CategoryCounts(ctx context.Context) (map[int64]int, error)
}

var wib = time.FixedZone("WIB", 7*3600)

func TestMemory(t *testing.T) {
	testBackend(t, func(t *testing.T) backend { return NewMemory() })
}

// TestPostgres runs the same behavioural tests against a real database. Each subtest gets
// its own throwaway schema, so pointing TEST_DATABASE_URL at a real database is safe.
func TestPostgres(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	testBackend(t, func(t *testing.T) backend { return openTestSchema(t, base) })

	t.Run("migrate is idempotent", func(t *testing.T) {
		p := openTestSchema(t, base)
		if err := p.Migrate(context.Background()); err != nil {
			t.Fatalf("second Migrate: %v", err)
		}
	})

	t.Run("batch rolls back on a database error", func(t *testing.T) {
		p := openTestSchema(t, base)
		ctx := context.Background()
		oct := time.Date(2026, 10, 5, 9, 0, 0, 0, wib)
		// Year 300000 is valid in Go but outside Postgres' timestamptz range, so the second
		// insert fails after the first one already ran inside the transaction.
		_, err := p.AddTransactions(ctx, []NewTransaction{
			{Kind: Income, Amount: 100, OccurredAt: oct},
			{Kind: Income, Amount: 200, OccurredAt: time.Date(300000, 1, 1, 0, 0, 0, 0, time.UTC)},
		})
		if err == nil {
			t.Fatal("want error")
		}
		got, _ := p.ListBetween(ctx, oct.AddDate(0, 0, -5), oct.AddDate(0, 1, 0))
		if len(got) != 0 {
			t.Errorf("%d rows saved, want 0 (rollback)", len(got))
		}
	})

	t.Run("check constraints", func(t *testing.T) {
		p := openTestSchema(t, base)
		ctx := context.Background()
		bad := []struct {
			kind   string
			amount int64
		}{{"bogus", 100}, {"income", 0}, {"expense", -5}}
		for _, b := range bad {
			_, err := p.pool.Exec(ctx,
				`INSERT INTO transactions (kind, amount_cents, occurred_at) VALUES ($1, $2, now())`,
				b.kind, b.amount)
			if err == nil {
				t.Errorf("insert (%q, %d) succeeded, want CHECK violation", b.kind, b.amount)
			}
		}
		_, err := p.pool.Exec(ctx,
			`INSERT INTO transactions (kind, amount_cents, occurred_at, note) VALUES ('income', 1, now(), $1)`,
			strings.Repeat("x", MaxNoteLen+1))
		if err == nil {
			t.Error("201-character note accepted by the database")
		}
	})

	t.Run("category foreign key checks the kind", func(t *testing.T) {
		p := openTestSchema(t, base)
		ctx := context.Background()
		var food, salary int64
		p.pool.QueryRow(ctx, `SELECT id FROM categories WHERE kind = 'expense' AND name = 'food'`).Scan(&food)
		p.pool.QueryRow(ctx, `SELECT id FROM categories WHERE kind = 'income' AND name = 'salary'`).Scan(&salary)
		if _, err := p.pool.Exec(ctx, `INSERT INTO transactions (kind, amount_cents, occurred_at, category_id)
			VALUES ('expense', 1, now(), $1)`, food); err != nil {
			t.Errorf("fitting category refused: %v", err)
		}
		if _, err := p.pool.Exec(ctx, `INSERT INTO transactions (kind, amount_cents, occurred_at, category_id)
			VALUES ('income', 1, now(), $1)`, food); err == nil {
			t.Error("expense category on an income entry accepted by the database")
		}
		if _, err := p.pool.Exec(ctx, `UPDATE transactions SET kind = 'income'`); err == nil {
			t.Error("kind changed away from its category")
		}
		for _, name := range []string{"Food", "none", "two words", "x123456789012345678901", "-x"} {
			if _, err := p.pool.Exec(ctx, `INSERT INTO categories (kind, name) VALUES ('expense', $1)`, name); err == nil {
				t.Errorf("category name %q accepted by the database", name)
			}
		}
		if err := p.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		var n int
		p.pool.QueryRow(ctx, `SELECT count(*) FROM categories`).Scan(&n)
		if n != 7 || salary == 0 {
			t.Errorf("%d categories after a second Migrate, want the 7 built-ins", n)
		}
	})
}

func openTestSchema(t *testing.T, base string) *Postgres {
	t.Helper()
	ctx := context.Background()

	suffix := make([]byte, 4)
	rand.Read(suffix)
	schema := "mm_test_" + hex.EncodeToString(suffix)

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL must be a URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	p, err := Open(ctx, u.String())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	if err := p.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return p
}

func testBackend(t *testing.T, newBackend func(t *testing.T) backend) {
	ctx := context.Background()
	oct := time.Date(2026, 10, 1, 0, 0, 0, 0, wib)
	nov := oct.AddDate(0, 1, 0)

	t.Run("add returns stored row", func(t *testing.T) {
		s := newBackend(t)
		at := time.Date(2026, 10, 17, 9, 0, 0, 0, wib)
		tx, err := s.AddTransaction(ctx, NewTransaction{Kind: Income, Amount: 500_000_000, OccurredAt: at})
		if err != nil {
			t.Fatal(err)
		}
		if tx.ID == 0 || tx.Kind != Income || tx.Amount != 500_000_000 || !tx.OccurredAt.Equal(at) {
			t.Errorf("got %+v", tx)
		}
		if tx.CreatedAt.IsZero() {
			t.Error("CreatedAt not set")
		}
	})

	t.Run("rejects invalid input", func(t *testing.T) {
		s := newBackend(t)
		if _, err := s.AddTransaction(ctx, NewTransaction{Kind: "bogus", Amount: 100, OccurredAt: oct}); err == nil {
			t.Error("bogus kind accepted")
		}
		if _, err := s.AddTransaction(ctx, NewTransaction{Kind: Expense, Amount: 0, OccurredAt: oct}); err == nil {
			t.Error("zero amount accepted")
		}
	})

	t.Run("list filters by month and sorts by occurred_at", func(t *testing.T) {
		s := newBackend(t)
		add := func(kind Kind, amount int64, at time.Time) {
			t.Helper()
			if _, err := s.AddTransaction(ctx, NewTransaction{Kind: kind, Amount: amount, OccurredAt: at}); err != nil {
				t.Fatal(err)
			}
		}
		add(Expense, 300, time.Date(2026, 10, 31, 23, 59, 0, 0, wib)) // last minute of Oct
		add(Income, 100, time.Date(2026, 11, 1, 0, 0, 0, 0, wib))     // first minute of Nov
		add(Income, 200, time.Date(2026, 10, 1, 0, 0, 0, 0, wib))     // first minute of Oct
		add(Expense, 400, time.Date(2026, 9, 30, 23, 59, 0, 0, wib))  // last minute of Sep
		add(Income, 250, time.Date(2026, 10, 1, 0, 0, 0, 0, wib))     // same time, later id

		got, err := s.ListBetween(ctx, oct, nov)
		if err != nil {
			t.Fatal(err)
		}
		want := []int64{200, 250, 300}
		if len(got) != len(want) {
			t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
		}
		for i, tx := range got {
			if tx.Amount != want[i] {
				t.Errorf("row %d amount = %d, want %d", i, tx.Amount, want[i])
			}
		}
	})

	t.Run("batch add is all or nothing", func(t *testing.T) {
		s := newBackend(t)
		a := time.Date(2026, 10, 2, 8, 0, 0, 0, wib)
		b := time.Date(2026, 10, 3, 19, 30, 0, 0, wib)
		saved, err := s.AddTransactions(ctx, []NewTransaction{
			{Kind: Income, Amount: 20_000_000, OccurredAt: a},
			{Kind: Expense, Amount: 5_500_000, OccurredAt: b},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(saved) != 2 || saved[0].ID == saved[1].ID || !saved[1].OccurredAt.Equal(b) {
			t.Errorf("saved = %+v", saved)
		}

		_, err = s.AddTransactions(ctx, []NewTransaction{
			{Kind: Income, Amount: 100, OccurredAt: a},
			{Kind: Expense, Amount: 0, OccurredAt: a},
		})
		if err == nil {
			t.Error("batch with a zero amount accepted")
		}
		got, _ := s.ListBetween(ctx, oct, nov)
		if len(got) != 2 {
			t.Errorf("%d rows after failed batch, want 2", len(got))
		}
	})

	seed := func(t *testing.T, s backend) []Transaction {
		t.Helper()
		saved, err := s.AddTransactions(ctx, []NewTransaction{
			{Kind: Income, Amount: 100, OccurredAt: time.Date(2026, 10, 2, 9, 0, 0, 0, wib)},
			{Kind: Expense, Amount: 200, OccurredAt: time.Date(2026, 10, 5, 12, 0, 0, 0, wib)},
			{Kind: Expense, Amount: 300, OccurredAt: time.Date(2026, 10, 5, 18, 0, 0, 0, wib)},
			{Kind: Income, Amount: 400, OccurredAt: time.Date(2026, 10, 9, 8, 0, 0, 0, wib)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	amounts := func(t *testing.T, s backend) []int64 {
		t.Helper()
		got, err := s.ListBetween(ctx, oct, nov)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, tx := range got {
			out = append(out, tx.Amount)
		}
		return out
	}
	equal := func(a, b []int64) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

	t.Run("soft delete hides entries; restore brings them back", func(t *testing.T) {
		s := newBackend(t)
		rows := seed(t, s)
		deleted, err := s.DeleteTransactions(ctx, []int64{rows[1].ID, rows[2].ID, rows[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(deleted) != 2 || deleted[0].DeletedAt.IsZero() {
			t.Errorf("deleted = %+v", deleted)
		}
		if got := amounts(t, s); !equal(got, []int64{100, 400}) {
			t.Errorf("after delete: %v", got)
		}
		trash, _ := s.ListDeleted(ctx)
		if len(trash) != 2 {
			t.Errorf("trash has %d entries", len(trash))
		}

		// Deleting again, or a missing id, fails as a whole.
		if _, err := s.DeleteTransactions(ctx, []int64{rows[0].ID, rows[1].ID}); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete already deleted: err = %v", err)
		}
		if _, err := s.DeleteTransactions(ctx, []int64{rows[0].ID, 999999}); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete missing: err = %v", err)
		}
		if got := amounts(t, s); !equal(got, []int64{100, 400}) {
			t.Errorf("failed delete changed rows: %v", got)
		}

		restored, err := s.RestoreTransactions(ctx, []int64{rows[2].ID})
		if err != nil || len(restored) != 1 || restored[0].ID != rows[2].ID || !restored[0].DeletedAt.IsZero() {
			t.Fatalf("restore = %+v, %v", restored, err)
		}
		if got := amounts(t, s); !equal(got, []int64{100, 300, 400}) {
			t.Errorf("after restore: %v", got)
		}
		if _, err := s.RestoreTransactions(ctx, []int64{rows[0].ID}); !errors.Is(err, ErrNotFound) {
			t.Errorf("restore active entry: err = %v", err)
		}
	})

	t.Run("nearest skips deleted entries", func(t *testing.T) {
		s := newBackend(t)
		rows := seed(t, s)
		day5 := time.Date(2026, 10, 5, 0, 0, 0, 0, wib)
		day6 := day5.AddDate(0, 0, 1)
		if at, ok, _ := s.Nearest(ctx, day5, true); !ok || !at.Equal(rows[0].OccurredAt) {
			t.Errorf("before day 5 = %v %v", at, ok)
		}
		if at, ok, _ := s.Nearest(ctx, day6, false); !ok || !at.Equal(rows[3].OccurredAt) {
			t.Errorf("from day 6 = %v %v", at, ok)
		}
		if at, ok, _ := s.Nearest(ctx, day5, false); !ok || !at.Equal(rows[1].OccurredAt) {
			t.Errorf("from day 5 (inclusive) = %v %v", at, ok)
		}
		s.DeleteTransactions(ctx, []int64{rows[3].ID})
		if _, ok, _ := s.Nearest(ctx, day6, false); ok {
			t.Error("found a deleted entry")
		}
		if _, ok, _ := s.Nearest(ctx, rows[0].OccurredAt, true); ok {
			t.Error("found an entry before the first one")
		}
	})

	t.Run("update checks the version", func(t *testing.T) {
		s := newBackend(t)
		rows := seed(t, s)
		newAt := time.Date(2026, 10, 6, 7, 30, 0, 0, wib)
		up, err := s.UpdateTransaction(ctx, rows[1].ID, NewTransaction{Kind: Income, Amount: 250, OccurredAt: newAt}, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if up.Kind != Income || up.Amount != 250 || !up.OccurredAt.Equal(newAt) || up.UpdatedAt.IsZero() || up.ID != rows[1].ID {
			t.Errorf("updated = %+v", up)
		}
		// Editing with the old version (someone else edited meanwhile) is refused.
		if _, err := s.UpdateTransaction(ctx, rows[1].ID, NewTransaction{Kind: Expense, Amount: 1, OccurredAt: newAt}, time.Time{}); !errors.Is(err, ErrChanged) {
			t.Errorf("stale update: err = %v", err)
		}
		// With the current version it works (this is how undo restores the old values).
		back, err := s.UpdateTransaction(ctx, rows[1].ID, NewTransaction{Kind: Expense, Amount: 200, OccurredAt: rows[1].OccurredAt}, up.UpdatedAt)
		if err != nil || back.Amount != 200 {
			t.Errorf("update with current version = %+v, %v", back, err)
		}
		// Back-to-back edits (within one clock tick) still get different versions.
		v := back.UpdatedAt
		for i := 0; i < 5; i++ {
			next, err := s.UpdateTransaction(ctx, rows[1].ID, NewTransaction{Kind: Expense, Amount: int64(300 + i), OccurredAt: rows[1].OccurredAt}, v)
			if err != nil {
				t.Fatalf("rapid edit %d: %v", i, err)
			}
			if !next.UpdatedAt.After(v) {
				t.Fatalf("rapid edit %d: version %v not after %v", i, next.UpdatedAt, v)
			}
			v = next.UpdatedAt
		}
		if _, err := s.UpdateTransaction(ctx, 999999, NewTransaction{Kind: Income, Amount: 1, OccurredAt: newAt}, time.Time{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing: err = %v", err)
		}
		s.DeleteTransactions(ctx, []int64{rows[0].ID})
		if _, err := s.UpdateTransaction(ctx, rows[0].ID, NewTransaction{Kind: Income, Amount: 1, OccurredAt: newAt}, time.Time{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("deleted: err = %v", err)
		}
		if _, err := s.UpdateTransaction(ctx, rows[2].ID, NewTransaction{Kind: Income, Amount: 0, OccurredAt: newAt}, time.Time{}); err == nil {
			t.Error("zero amount accepted")
		}
	})

	t.Run("notes are saved, edited and kept through delete and restore", func(t *testing.T) {
		s := newBackend(t)
		at := time.Date(2026, 10, 17, 12, 10, 0, 0, wib)
		tx, err := s.AddTransaction(ctx, NewTransaction{Kind: Expense, Amount: 2_500_000, OccurredAt: at, Note: "makan siang ☕"})
		if err != nil || tx.Note != "makan siang ☕" {
			t.Fatalf("add = %+v, %v", tx, err)
		}
		batch, err := s.AddTransactions(ctx, []NewTransaction{
			{Kind: Income, Amount: 100, OccurredAt: at, Note: "gaji oktober"},
			{Kind: Income, Amount: 200, OccurredAt: at},
		})
		if err != nil || batch[0].Note != "gaji oktober" || batch[1].Note != "" {
			t.Fatalf("batch = %+v, %v", batch, err)
		}

		v := tx.Values()
		v.Note = "makan siang kantor"
		up, err := s.UpdateTransaction(ctx, tx.ID, v, tx.UpdatedAt)
		if err != nil || up.Note != "makan siang kantor" || up.Amount != tx.Amount {
			t.Fatalf("update = %+v, %v", up, err)
		}
		v.Note = ""
		cleared, err := s.UpdateTransaction(ctx, tx.ID, v, up.UpdatedAt)
		if err != nil || cleared.Note != "" {
			t.Fatalf("clear = %+v, %v", cleared, err)
		}

		s.DeleteTransactions(ctx, []int64{batch[0].ID})
		trash, _ := s.ListDeleted(ctx)
		if len(trash) != 1 || trash[0].Note != "gaji oktober" {
			t.Errorf("trash = %+v", trash)
		}
		restored, _ := s.RestoreTransactions(ctx, []int64{batch[0].ID})
		if len(restored) != 1 || restored[0].Note != "gaji oktober" {
			t.Errorf("restored = %+v", restored)
		}
		got, _ := s.ListBetween(ctx, oct, nov)
		notes := map[int64]string{}
		for _, r := range got {
			notes[r.ID] = r.Note
		}
		if notes[batch[0].ID] != "gaji oktober" || notes[tx.ID] != "" {
			t.Errorf("listed notes = %v", notes)
		}
	})

	t.Run("note length is limited", func(t *testing.T) {
		s := newBackend(t)
		ok := strings.Repeat("é", MaxNoteLen) // characters, not bytes
		if _, err := s.AddTransaction(ctx, NewTransaction{Kind: Income, Amount: 1, OccurredAt: oct, Note: ok}); err != nil {
			t.Errorf("200-character note rejected: %v", err)
		}
		long := ok + "x"
		if _, err := s.AddTransaction(ctx, NewTransaction{Kind: Income, Amount: 1, OccurredAt: oct, Note: long}); err == nil {
			t.Error("201-character note accepted")
		}
	})

	t.Run("purge removes only old deletions", func(t *testing.T) {
		s := newBackend(t)
		rows := seed(t, s)
		s.DeleteTransactions(ctx, []int64{rows[0].ID, rows[1].ID})
		n, err := s.PurgeDeleted(ctx, time.Now().Add(-time.Hour))
		if err != nil || n != 0 {
			t.Errorf("purge of recent deletions = %d, %v", n, err)
		}
		n, err = s.PurgeDeleted(ctx, time.Now().Add(time.Hour))
		if err != nil || n != 2 {
			t.Errorf("purge = %d, %v; want 2", n, err)
		}
		if trash, _ := s.ListDeleted(ctx); len(trash) != 0 {
			t.Errorf("trash after purge: %d", len(trash))
		}
		if got := amounts(t, s); !equal(got, []int64{300, 400}) {
			t.Errorf("active rows after purge: %v", got)
		}
	})

	t.Run("empty month", func(t *testing.T) {
		s := newBackend(t)
		got, err := s.ListBetween(ctx, oct, nov)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("got %d rows, want 0", len(got))
		}
	})

	testCategories(t, newBackend)
}
