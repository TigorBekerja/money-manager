CREATE TABLE IF NOT EXISTS categories (
    id         BIGSERIAL   PRIMARY KEY,
    kind       TEXT        NOT NULL CHECK (kind IN ('income', 'expense')),
    name       TEXT        NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,19}$' AND name <> 'none'),
    builtin    BOOLEAN     NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, name),
    UNIQUE (id, kind) -- target of the composite foreign key below
);

-- Built-ins, in the order they are listed (store.builtinCategories).
INSERT INTO categories (kind, name, builtin) VALUES
    ('expense', 'food', true),
    ('expense', 'drink', true),
    ('expense', 'dessert', true),
    ('expense', 'laundry', true),
    ('expense', 'subscription', true),
    ('expense', 'transport', true),
    ('income', 'salary', true)
ON CONFLICT (kind, name) DO NOTHING;

-- NULL = no category. The key includes kind, so an expense category can't be put on an
-- income entry (and changing an entry's kind requires a fitting category).
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS category_id BIGINT;
ALTER TABLE transactions ADD CONSTRAINT transactions_category_fk
    FOREIGN KEY (category_id, kind) REFERENCES categories (id, kind);
CREATE INDEX IF NOT EXISTS transactions_category_idx ON transactions (category_id);
