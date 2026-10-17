package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Entries are read as "t" joined with their category "c" for its current name.
const (
	columns = "t.id, t.kind, t.amount_cents, t.occurred_at, t.note, t.category_id, c.name, " +
		"t.created_at, t.updated_at, t.deleted_at"
	fromEntries = " FROM transactions t LEFT JOIN categories c ON c.id = t.category_id"
)

// returning wraps an INSERT/UPDATE so its rows come back with the category name.
func returning(modify string) string {
	return "WITH t AS (" + modify + " RETURNING *) SELECT " + columns + " FROM t LEFT JOIN categories c ON c.id = t.category_id"
}

var insertSQL = returning(`
	INSERT INTO transactions (kind, amount_cents, occurred_at, note, category_id) VALUES ($1, $2, $3, $4, $5)`)

// categoryArg turns 0 (no category) into NULL.
func categoryArg(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// pgCode returns the Postgres error code of err, or "".
func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// categoryErr maps a foreign key violation (a missing category, or one of the other kind)
// to ErrCategoryKind.
func categoryErr(err error) error {
	if pgCode(err) == "23503" { // foreign_key_violation
		return ErrCategoryKind
	}
	return err
}

func (p *Postgres) AddTransaction(ctx context.Context, v NewTransaction) (Transaction, error) {
	if err := validate(v); err != nil {
		return Transaction{}, err
	}
	row := p.pool.QueryRow(ctx, insertSQL, string(v.Kind), v.Amount, v.OccurredAt, v.Note, categoryArg(v.CategoryID))
	t, err := scanTransaction(row)
	return t, categoryErr(err)
}

// AddTransactions inserts all rows in one database transaction: all or nothing.
func (p *Postgres) AddTransactions(ctx context.Context, rows []NewTransaction) ([]Transaction, error) {
	for _, r := range rows {
		if err := validate(r); err != nil {
			return nil, err
		}
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	out := make([]Transaction, 0, len(rows))
	for _, r := range rows {
		row := tx.QueryRow(ctx, insertSQL, string(r.Kind), r.Amount, r.OccurredAt, r.Note, categoryArg(r.CategoryID))
		t, err := scanTransaction(row)
		if err != nil {
			return nil, categoryErr(err)
		}
		out = append(out, t)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// ListBetween returns active (not deleted) entries with from <= occurred_at < to.
func (p *Postgres) ListBetween(ctx context.Context, from, to time.Time) ([]Transaction, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+columns+fromEntries+`
		WHERE t.occurred_at >= $1 AND t.occurred_at < $2 AND t.deleted_at IS NULL
		ORDER BY t.occurred_at, t.id`, from, to)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// Nearest returns the time of the closest active entry before t, or at/after t.
func (p *Postgres) Nearest(ctx context.Context, t time.Time, before bool) (time.Time, bool, error) {
	q := `SELECT min(occurred_at) FROM transactions WHERE occurred_at >= $1 AND deleted_at IS NULL`
	if before {
		q = `SELECT max(occurred_at) FROM transactions WHERE occurred_at < $1 AND deleted_at IS NULL`
	}
	var at *time.Time
	if err := p.pool.QueryRow(ctx, q, t).Scan(&at); err != nil {
		return time.Time{}, false, err
	}
	if at == nil {
		return time.Time{}, false, nil
	}
	return *at, true, nil
}

// nextVersion is the new updated_at of an edited entry: it must change on every edit,
// even within one clock tick.
const nextVersion = `GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')`

// UpdateTransaction changes an active entry whose updated_at still equals expectUpdatedAt
// (zero = never edited). ErrNotFound if it's gone or deleted, ErrChanged if edited since.
func (p *Postgres) UpdateTransaction(ctx context.Context, id int64, v NewTransaction, expectUpdatedAt time.Time) (Transaction, error) {
	if err := validate(v); err != nil {
		return Transaction{}, err
	}
	var expect *time.Time
	if !expectUpdatedAt.IsZero() {
		expect = &expectUpdatedAt
	}
	row := p.pool.QueryRow(ctx, returning(`
		UPDATE transactions
		SET kind = $2, amount_cents = $3, occurred_at = $4, note = $6, category_id = $7,
		    updated_at = `+nextVersion+`
		WHERE id = $1 AND deleted_at IS NULL AND updated_at IS NOT DISTINCT FROM $5`),
		id, string(v.Kind), v.Amount, v.OccurredAt, expect, v.Note, categoryArg(v.CategoryID))
	t, err := scanTransaction(row)
	if err == pgx.ErrNoRows {
		var exists bool
		if err := p.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM transactions WHERE id = $1 AND deleted_at IS NULL)`, id).Scan(&exists); err != nil {
			return Transaction{}, err
		}
		if exists {
			return Transaction{}, ErrChanged
		}
		return Transaction{}, ErrNotFound
	}
	return t, categoryErr(err)
}

// DeleteTransactions moves active entries to the trash: all of them or none.
func (p *Postgres) DeleteTransactions(ctx context.Context, ids []int64) ([]Transaction, error) {
	return p.setDeleted(ctx, ids, returning(`
		UPDATE transactions SET deleted_at = clock_timestamp()
		WHERE id = ANY($1) AND deleted_at IS NULL`))
}

// RestoreTransactions brings entries back from the trash: all of them or none.
func (p *Postgres) RestoreTransactions(ctx context.Context, ids []int64) ([]Transaction, error) {
	return p.setDeleted(ctx, ids, returning(`
		UPDATE transactions SET deleted_at = NULL
		WHERE id = ANY($1) AND deleted_at IS NOT NULL`))
}

func (p *Postgres) setDeleted(ctx context.Context, ids []int64, q string) ([]Transaction, error) {
	ids = uniqueIDs(ids)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out, err := collect(rows)
	if err != nil {
		return nil, err
	}
	if len(out) != len(ids) {
		return nil, ErrNotFound // rolled back
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// ListDeleted returns the trash, most recently deleted first.
func (p *Postgres) ListDeleted(ctx context.Context) ([]Transaction, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+columns+fromEntries+`
		WHERE t.deleted_at IS NOT NULL
		ORDER BY t.deleted_at DESC, t.id`)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// PurgeDeleted permanently removes entries deleted before the cutoff.
func (p *Postgres) PurgeDeleted(ctx context.Context, before time.Time) (int, error) {
	tag, err := p.pool.Exec(ctx, `DELETE FROM transactions WHERE deleted_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func collect(rows pgx.Rows) ([]Transaction, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Transaction, error) {
		return scanTransaction(r)
	})
}

func scanTransaction(row pgx.Row) (Transaction, error) {
	var tx Transaction
	var kind string
	var categoryID *int64
	var category *string
	var updated, deleted *time.Time
	if err := row.Scan(&tx.ID, &kind, &tx.Amount, &tx.OccurredAt, &tx.Note, &categoryID, &category,
		&tx.CreatedAt, &updated, &deleted); err != nil {
		return Transaction{}, err
	}
	tx.Kind = Kind(kind)
	if categoryID != nil {
		tx.CategoryID = *categoryID
	}
	if category != nil {
		tx.Category = *category
	}
	if updated != nil {
		tx.UpdatedAt = *updated
	}
	if deleted != nil {
		tx.DeletedAt = *deleted
	}
	return tx, nil
}

// Categories.

const categoryColumns = "id, kind, name, builtin"

func scanCategory(row pgx.Row) (Category, error) {
	var c Category
	var kind string
	if err := row.Scan(&c.ID, &kind, &c.Name, &c.Builtin); err != nil {
		return Category{}, err
	}
	c.Kind = Kind(kind)
	return c, nil
}

// Categories returns all categories in display order (SortCategories).
func (p *Postgres) Categories(ctx context.Context) ([]Category, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+categoryColumns+` FROM categories`)
	if err != nil {
		return nil, err
	}
	cats, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Category, error) { return scanCategory(r) })
	if err != nil {
		return nil, err
	}
	SortCategories(cats)
	return cats, nil
}

// AddCategory adds a user category.
func (p *Postgres) AddCategory(ctx context.Context, kind Kind, name string) (Category, error) {
	if !kind.Valid() {
		return Category{}, ErrCategoryKind
	}
	cats, err := p.Categories(ctx)
	if err != nil {
		return Category{}, err
	}
	if err := CheckCategoryName(cats, kind, name, 0); err != nil {
		return Category{}, err
	}
	c, err := scanCategory(p.pool.QueryRow(ctx,
		`INSERT INTO categories (kind, name) VALUES ($1, $2) RETURNING `+categoryColumns, string(kind), name))
	if pgCode(err) == "23505" { // unique_violation: added from another window just now
		return Category{}, ErrCategoryExists
	}
	return c, err
}

// lockCategory reads a category inside tx and locks it; ErrNotFound / ErrBuiltin.
func lockCategory(ctx context.Context, tx pgx.Tx, id int64) (Category, error) {
	c, err := scanCategory(tx.QueryRow(ctx, `SELECT `+categoryColumns+` FROM categories WHERE id = $1 FOR UPDATE`, id))
	switch {
	case err == pgx.ErrNoRows:
		return Category{}, ErrNotFound
	case err != nil:
		return Category{}, err
	case c.Builtin:
		return Category{}, ErrBuiltin
	}
	return c, nil
}

// RenameCategory gives a user category a new name; entries follow automatically.
func (p *Postgres) RenameCategory(ctx context.Context, id int64, name string) (Category, error) {
	cats, err := p.Categories(ctx)
	if err != nil {
		return Category{}, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback(ctx)
	c, err := lockCategory(ctx, tx, id)
	if err != nil {
		return Category{}, err
	}
	if err := CheckCategoryName(cats, c.Kind, name, id); err != nil {
		return Category{}, err
	}
	c, err = scanCategory(tx.QueryRow(ctx,
		`UPDATE categories SET name = $2 WHERE id = $1 RETURNING `+categoryColumns, id, name))
	if pgCode(err) == "23505" {
		return Category{}, ErrCategoryExists
	}
	if err != nil {
		return Category{}, err
	}
	return c, tx.Commit(ctx)
}

// RemoveCategory deletes a user category. Its entries (in the trash too) move to moveTo,
// a category of the same kind, or to none when moveTo is 0. It returns how many moved.
func (p *Postgres) RemoveCategory(ctx context.Context, id, moveTo int64) (int, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	c, err := lockCategory(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	if moveTo != 0 {
		var kind string
		err := tx.QueryRow(ctx, `SELECT kind FROM categories WHERE id = $1 FOR SHARE`, moveTo).Scan(&kind)
		if err == pgx.ErrNoRows || err == nil && (moveTo == id || Kind(kind) != c.Kind) {
			return 0, ErrCategoryKind
		}
		if err != nil {
			return 0, err
		}
	}
	// Moving changes the entries, so their version changes too (edits open elsewhere and
	// undo notice it). The foreign key refuses a target of the other kind.
	tag, err := tx.Exec(ctx, `UPDATE transactions SET category_id = $2, updated_at = `+nextVersion+`
		WHERE category_id = $1`, id, categoryArg(moveTo))
	if err != nil {
		return 0, categoryErr(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM categories WHERE id = $1`, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// CategoryCounts returns the number of active entries per category id.
func (p *Postgres) CategoryCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := p.pool.Query(ctx, `SELECT category_id, count(*) FROM transactions
		WHERE category_id IS NOT NULL AND deleted_at IS NULL GROUP BY category_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
