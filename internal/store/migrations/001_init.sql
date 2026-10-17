CREATE TABLE IF NOT EXISTS transactions (
    id           BIGSERIAL   PRIMARY KEY,
    kind         TEXT        NOT NULL CHECK (kind IN ('income', 'expense')),
    amount_cents BIGINT      NOT NULL CHECK (amount_cents > 0),
    occurred_at  TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS transactions_occurred_at_idx ON transactions (occurred_at);
