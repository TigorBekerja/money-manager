// Package store persists transactions. Postgres is the real backend; Memory backs the
// tutorial and tests and must behave the same way.
package store

import (
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"
)

type Kind string

const (
	Income  Kind = "income"
	Expense Kind = "expense"
)

func (k Kind) Valid() bool { return k == Income || k == Expense }

type Transaction struct {
	ID         int64
	Kind       Kind
	Amount     int64     // minor units (cents)
	OccurredAt time.Time // when the money moved (user-given or now)
	Note       string    // what it was for; "" if none
	CategoryID int64     // 0 = no category
	Category   string    // the category's current name; "" if none
	CreatedAt  time.Time // when the row was inserted (audit only)
	UpdatedAt  time.Time // last edit; zero if never edited
	DeletedAt  time.Time // when moved to trash; zero if active
}

// Values returns the editable fields of t.
func (t Transaction) Values() NewTransaction {
	return NewTransaction{Kind: t.Kind, Amount: t.Amount, OccurredAt: t.OccurredAt, Note: t.Note, CategoryID: t.CategoryID}
}

// TrashDays is how long deleted entries can be restored before they are purged.
const TrashDays = 30

// MaxNoteLen is the longest note, in characters.
const MaxNoteLen = 200

var (
	// ErrNotFound means an entry doesn't exist or isn't in the expected state (active for
	// edit/delete, deleted for restore), e.g. because it was changed from another window.
	ErrNotFound = errors.New("entry not found")
	// ErrChanged means an entry was edited since it was read.
	ErrChanged = errors.New("entry changed since it was shown")
)

// NewTransaction holds the values of an entry to add or to save over an existing one.
type NewTransaction struct {
	Kind       Kind
	Amount     int64
	OccurredAt time.Time
	Note       string
	CategoryID int64 // 0 = none; must be a category of Kind (ErrCategoryKind otherwise)
}

// uniqueIDs drops duplicates, keeping order.
func uniqueIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func validate(v NewTransaction) error {
	if !v.Kind.Valid() {
		return fmt.Errorf("invalid transaction kind %q", v.Kind)
	}
	if v.Amount <= 0 {
		return fmt.Errorf("amount must be positive, got %d", v.Amount)
	}
	if utf8.RuneCountInString(v.Note) > MaxNoteLen {
		return fmt.Errorf("note is longer than %d characters", MaxNoteLen)
	}
	return nil
}
