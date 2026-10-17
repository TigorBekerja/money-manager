ALTER TABLE transactions ADD COLUMN IF NOT EXISTS note TEXT NOT NULL DEFAULT '';
ALTER TABLE transactions ADD CONSTRAINT transactions_note_length CHECK (char_length(note) <= 200);
