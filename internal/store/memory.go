package store

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Memory is an in-memory store with the same behaviour as Postgres.
type Memory struct {
	mu     sync.Mutex
	nextID int64
	txs    []Transaction
}

func NewMemory() *Memory { return &Memory{nextID: 1} }

// now matches Postgres' microsecond precision so timestamps compare the same way.
func now() time.Time { return time.Now().Truncate(time.Microsecond) }

func (m *Memory) AddTransaction(_ context.Context, kind Kind, amount int64, occurredAt time.Time) (Transaction, error) {
	if err := validate(kind, amount); err != nil {
		return Transaction{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.insert(kind, amount, occurredAt), nil
}

func (m *Memory) insert(kind Kind, amount int64, occurredAt time.Time) Transaction {
	tx := Transaction{ID: m.nextID, Kind: kind, Amount: amount, OccurredAt: occurredAt, CreatedAt: now()}
	m.nextID++
	m.txs = append(m.txs, tx)
	return tx
}

// AddTransactions stores all rows or none.
func (m *Memory) AddTransactions(_ context.Context, rows []NewTransaction) ([]Transaction, error) {
	for _, r := range rows {
		if err := validate(r.Kind, r.Amount); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Transaction, 0, len(rows))
	for _, r := range rows {
		out = append(out, m.insert(r.Kind, r.Amount, r.OccurredAt))
	}
	return out, nil
}

func (m *Memory) ListBetween(_ context.Context, from, to time.Time) ([]Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Transaction
	for _, tx := range m.txs {
		if tx.DeletedAt.IsZero() && !tx.OccurredAt.Before(from) && tx.OccurredAt.Before(to) {
			out = append(out, tx)
		}
	}
	slices.SortFunc(out, func(a, b Transaction) int {
		if c := a.OccurredAt.Compare(b.OccurredAt); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})
	return out, nil
}

// Nearest returns the time of the closest active entry before t, or at/after t.
func (m *Memory) Nearest(_ context.Context, t time.Time, before bool) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best time.Time
	found := false
	for _, tx := range m.txs {
		if !tx.DeletedAt.IsZero() {
			continue
		}
		at := tx.OccurredAt
		if before && at.Before(t) && (!found || at.After(best)) ||
			!before && !at.Before(t) && (!found || at.Before(best)) {
			best, found = at, true
		}
	}
	return best, found, nil
}

func (m *Memory) find(id int64) int {
	return slices.IndexFunc(m.txs, func(tx Transaction) bool { return tx.ID == id })
}

// UpdateTransaction changes an active entry whose UpdatedAt still equals expectUpdatedAt.
func (m *Memory) UpdateTransaction(_ context.Context, id int64, kind Kind, amount int64, occurredAt, expectUpdatedAt time.Time) (Transaction, error) {
	if err := validate(kind, amount); err != nil {
		return Transaction{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.find(id)
	if i < 0 || !m.txs[i].DeletedAt.IsZero() {
		return Transaction{}, ErrNotFound
	}
	if !m.txs[i].UpdatedAt.Equal(expectUpdatedAt) {
		return Transaction{}, ErrChanged
	}
	tx := &m.txs[i]
	// The version must change on every edit, even within one clock tick.
	updated := now()
	if !updated.After(tx.UpdatedAt) {
		updated = tx.UpdatedAt.Add(time.Microsecond)
	}
	tx.Kind, tx.Amount, tx.OccurredAt, tx.UpdatedAt = kind, amount, occurredAt, updated
	return *tx, nil
}

// DeleteTransactions moves active entries to the trash: all of them or none.
func (m *Memory) DeleteTransactions(_ context.Context, ids []int64) ([]Transaction, error) {
	return m.setDeleted(ids, true)
}

// RestoreTransactions brings entries back from the trash: all of them or none.
func (m *Memory) RestoreTransactions(_ context.Context, ids []int64) ([]Transaction, error) {
	return m.setDeleted(ids, false)
}

func (m *Memory) setDeleted(ids []int64, deleted bool) ([]Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids = uniqueIDs(ids)
	idx := make([]int, 0, len(ids))
	for _, id := range ids {
		i := m.find(id)
		if i < 0 || m.txs[i].DeletedAt.IsZero() != deleted {
			return nil, ErrNotFound
		}
		idx = append(idx, i)
	}
	at := now()
	out := make([]Transaction, 0, len(idx))
	for _, i := range idx {
		if deleted {
			m.txs[i].DeletedAt = at
		} else {
			m.txs[i].DeletedAt = time.Time{}
		}
		out = append(out, m.txs[i])
	}
	return out, nil
}

// ListDeleted returns the trash, most recently deleted first.
func (m *Memory) ListDeleted(context.Context) ([]Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Transaction
	for _, tx := range m.txs {
		if !tx.DeletedAt.IsZero() {
			out = append(out, tx)
		}
	}
	slices.SortFunc(out, func(a, b Transaction) int {
		if c := b.DeletedAt.Compare(a.DeletedAt); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})
	return out, nil
}

// PurgeDeleted permanently removes entries deleted before the cutoff.
func (m *Memory) PurgeDeleted(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.txs)
	m.txs = slices.DeleteFunc(m.txs, func(tx Transaction) bool {
		return !tx.DeletedAt.IsZero() && tx.DeletedAt.Before(before)
	})
	return n - len(m.txs), nil
}

// Len reports how many transactions have been stored, including deleted ones.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.txs)
}

// SetDeletedAt backdates a deletion (tests of the 30-day purge).
func (m *Memory) SetDeletedAt(id int64, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i := m.find(id); i >= 0 {
		m.txs[i].DeletedAt = at
	}
}
