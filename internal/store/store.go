// Package store persists transactions. Postgres is the real backend; Memory backs the
// tutorial and tests and must behave the same way.
package store

import (
	"errors"
	"fmt"
	"slices"
	"time"
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
	CreatedAt  time.Time // when the row was inserted (audit only)
	UpdatedAt  time.Time // last edit; zero if never edited
	DeletedAt  time.Time // when moved to trash; zero if active
}

// TrashDays is how long deleted entries can be restored before they are purged.
const TrashDays = 30

var (
	// ErrNotFound means an entry doesn't exist or isn't in the expected state (active for
	// edit/delete, deleted for restore), e.g. because it was changed from another window.
	ErrNotFound = errors.New("entry not found")
	// ErrChanged means an entry was edited since it was read.
	ErrChanged = errors.New("entry changed since it was shown")
)

// NewTransaction is one row for AddTransactions.
type NewTransaction struct {
	Kind       Kind
	Amount     int64
	OccurredAt time.Time
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

func validate(kind Kind, amount int64) error {
	if !kind.Valid() {
		return fmt.Errorf("invalid transaction kind %q", kind)
	}
	if amount <= 0 {
		return fmt.Errorf("amount must be positive, got %d", amount)
	}
	return nil
}
