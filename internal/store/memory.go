package store

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Memory is an in-memory store with the same behaviour as Postgres.
type Memory struct {
	mu        sync.Mutex
	nextID    int64
	txs       []Transaction
	nextCatID int64
	cats      []Category
}

// NewMemory returns an empty store with the built-in categories, like a migrated database.
func NewMemory() *Memory {
	m := &Memory{nextID: 1, nextCatID: 1}
	for _, b := range builtinCategories {
		m.cats = append(m.cats, Category{ID: m.nextCatID, Kind: b.kind, Name: b.name, Builtin: true})
		m.nextCatID++
	}
	return m
}

// now matches Postgres' microsecond precision so timestamps compare the same way.
func now() time.Time { return time.Now().Truncate(time.Microsecond) }

func (m *Memory) AddTransaction(_ context.Context, v NewTransaction) (Transaction, error) {
	if err := validate(v); err != nil {
		return Transaction{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkCategory(v); err != nil {
		return Transaction{}, err
	}
	return m.named(m.insert(v)), nil
}

func (m *Memory) insert(v NewTransaction) Transaction {
	tx := Transaction{ID: m.nextID, Kind: v.Kind, Amount: v.Amount, OccurredAt: v.OccurredAt, Note: v.Note,
		CategoryID: v.CategoryID, CreatedAt: now()}
	m.nextID++
	m.txs = append(m.txs, tx)
	return tx
}

// checkCategory is the foreign key (category_id, kind) of the database.
func (m *Memory) checkCategory(v NewTransaction) error {
	if v.CategoryID == 0 {
		return nil
	}
	i := m.findCategory(v.CategoryID)
	if i < 0 || m.cats[i].Kind != v.Kind {
		return ErrCategoryKind
	}
	return nil
}

// named fills in the category name, like the join in the Postgres queries.
func (m *Memory) named(tx Transaction) Transaction {
	tx.Category = ""
	if i := m.findCategory(tx.CategoryID); i >= 0 {
		tx.Category = m.cats[i].Name
	}
	return tx
}

// AddTransactions stores all rows or none.
func (m *Memory) AddTransactions(_ context.Context, rows []NewTransaction) ([]Transaction, error) {
	for _, r := range rows {
		if err := validate(r); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range rows {
		if err := m.checkCategory(r); err != nil {
			return nil, err
		}
	}
	out := make([]Transaction, 0, len(rows))
	for _, r := range rows {
		out = append(out, m.named(m.insert(r)))
	}
	return out, nil
}

func (m *Memory) ListBetween(_ context.Context, from, to time.Time) ([]Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Transaction
	for _, tx := range m.txs {
		if tx.DeletedAt.IsZero() && !tx.OccurredAt.Before(from) && tx.OccurredAt.Before(to) {
			out = append(out, m.named(tx))
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

// bump gives tx a new version: it must change on every edit, even within one clock tick.
func bump(tx *Transaction) {
	updated := now()
	if !updated.After(tx.UpdatedAt) {
		updated = tx.UpdatedAt.Add(time.Microsecond)
	}
	tx.UpdatedAt = updated
}

// UpdateTransaction changes an active entry whose UpdatedAt still equals expectUpdatedAt.
func (m *Memory) UpdateTransaction(_ context.Context, id int64, v NewTransaction, expectUpdatedAt time.Time) (Transaction, error) {
	if err := validate(v); err != nil {
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
	if err := m.checkCategory(v); err != nil {
		return Transaction{}, err
	}
	tx := &m.txs[i]
	tx.Kind, tx.Amount, tx.OccurredAt, tx.Note, tx.CategoryID = v.Kind, v.Amount, v.OccurredAt, v.Note, v.CategoryID
	bump(tx)
	return m.named(*tx), nil
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
		out = append(out, m.named(m.txs[i]))
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
			out = append(out, m.named(tx))
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

func (m *Memory) findCategory(id int64) int {
	if id == 0 {
		return -1
	}
	return slices.IndexFunc(m.cats, func(c Category) bool { return c.ID == id })
}

// Categories returns all categories in display order (SortCategories).
func (m *Memory) Categories(context.Context) ([]Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.cats)
	SortCategories(out)
	return out, nil
}

// AddCategory adds a user category.
func (m *Memory) AddCategory(_ context.Context, kind Kind, name string) (Category, error) {
	if !kind.Valid() {
		return Category{}, ErrCategoryKind
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := CheckCategoryName(m.cats, kind, name, 0); err != nil {
		return Category{}, err
	}
	c := Category{ID: m.nextCatID, Kind: kind, Name: name}
	m.nextCatID++
	m.cats = append(m.cats, c)
	return c, nil
}

// RenameCategory gives a user category a new name; entries follow automatically.
func (m *Memory) RenameCategory(_ context.Context, id int64, name string) (Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findCategory(id)
	switch {
	case i < 0:
		return Category{}, ErrNotFound
	case m.cats[i].Builtin:
		return Category{}, ErrBuiltin
	}
	if err := CheckCategoryName(m.cats, m.cats[i].Kind, name, id); err != nil {
		return Category{}, err
	}
	m.cats[i].Name = name
	return m.cats[i], nil
}

// RemoveCategory deletes a user category. Its entries (in the trash too) move to moveTo,
// a category of the same kind, or to none when moveTo is 0. It returns how many moved.
func (m *Memory) RemoveCategory(_ context.Context, id, moveTo int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.findCategory(id)
	switch {
	case i < 0:
		return 0, ErrNotFound
	case m.cats[i].Builtin:
		return 0, ErrBuiltin
	}
	if moveTo != 0 {
		j := m.findCategory(moveTo)
		if j < 0 || moveTo == id || m.cats[j].Kind != m.cats[i].Kind {
			return 0, ErrCategoryKind
		}
	}
	moved := 0
	for k := range m.txs {
		if m.txs[k].CategoryID == id {
			m.txs[k].CategoryID = moveTo
			bump(&m.txs[k])
			moved++
		}
	}
	m.cats = slices.Delete(m.cats, i, i+1)
	return moved, nil
}

// CategoryCounts returns the number of active entries per category id.
func (m *Memory) CategoryCounts(context.Context) (map[int64]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[int64]int{}
	for _, tx := range m.txs {
		if tx.DeletedAt.IsZero() && tx.CategoryID != 0 {
			out[tx.CategoryID]++
		}
	}
	return out, nil
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
