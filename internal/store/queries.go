package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

const columns = "id, kind, amount_cents, occurred_at, created_at, updated_at, deleted_at"

func (p *Postgres) AddTransaction(ctx context.Context, kind Kind, amount int64, occurredAt time.Time) (Transaction, error) {
	if err := validate(kind, amount); err != nil {
		return Transaction{}, err
	}
	row := p.pool.QueryRow(ctx, `
		INSERT INTO transactions (kind, amount_cents, occurred_at) VALUES ($1, $2, $3)
		RETURNING `+columns, string(kind), amount, occurredAt)
	return scanTransaction(row)
}

// AddTransactions inserts all rows in one database transaction: all or nothing.
func (p *Postgres) AddTransactions(ctx context.Context, rows []NewTransaction) ([]Transaction, error) {
	for _, r := range rows {
		if err := validate(r.Kind, r.Amount); err != nil {
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
		row := tx.QueryRow(ctx, `
			INSERT INTO transactions (kind, amount_cents, occurred_at) VALUES ($1, $2, $3)
			RETURNING `+columns, string(r.Kind), r.Amount, r.OccurredAt)
		t, err := scanTransaction(row)
		if err != nil {
			return nil, err
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
	rows, err := p.pool.Query(ctx, `
		SELECT `+columns+`
		FROM transactions
		WHERE occurred_at >= $1 AND occurred_at < $2 AND deleted_at IS NULL
		ORDER BY occurred_at, id`, from, to)
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

// UpdateTransaction changes an active entry whose updated_at still equals expectUpdatedAt
// (zero = never edited). ErrNotFound if it's gone or deleted, ErrChanged if edited since.
func (p *Postgres) UpdateTransaction(ctx context.Context, id int64, kind Kind, amount int64, occurredAt, expectUpdatedAt time.Time) (Transaction, error) {
	if err := validate(kind, amount); err != nil {
		return Transaction{}, err
	}
	var expect *time.Time
	if !expectUpdatedAt.IsZero() {
		expect = &expectUpdatedAt
	}
	row := p.pool.QueryRow(ctx, `
		UPDATE transactions
		SET kind = $2, amount_cents = $3, occurred_at = $4,
		    -- the version must change on every edit, even within one clock tick
		    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
		WHERE id = $1 AND deleted_at IS NULL AND updated_at IS NOT DISTINCT FROM $5
		RETURNING `+columns, id, string(kind), amount, occurredAt, expect)
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
	return t, err
}

// DeleteTransactions moves active entries to the trash: all of them or none.
func (p *Postgres) DeleteTransactions(ctx context.Context, ids []int64) ([]Transaction, error) {
	return p.setDeleted(ctx, ids, `
		UPDATE transactions SET deleted_at = clock_timestamp()
		WHERE id = ANY($1) AND deleted_at IS NULL
		RETURNING `+columns)
}

// RestoreTransactions brings entries back from the trash: all of them or none.
func (p *Postgres) RestoreTransactions(ctx context.Context, ids []int64) ([]Transaction, error) {
	return p.setDeleted(ctx, ids, `
		UPDATE transactions SET deleted_at = NULL
		WHERE id = ANY($1) AND deleted_at IS NOT NULL
		RETURNING `+columns)
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
	rows, err := p.pool.Query(ctx, `
		SELECT `+columns+` FROM transactions
		WHERE deleted_at IS NOT NULL
		ORDER BY deleted_at DESC, id`)
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
	var updated, deleted *time.Time
	if err := row.Scan(&tx.ID, &kind, &tx.Amount, &tx.OccurredAt, &tx.CreatedAt, &updated, &deleted); err != nil {
		return Transaction{}, err
	}
	tx.Kind = Kind(kind)
	if updated != nil {
		tx.UpdatedAt = *updated
	}
	if deleted != nil {
		tx.DeletedAt = *deleted
	}
	return tx, nil
}
