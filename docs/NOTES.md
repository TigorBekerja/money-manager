# Design: notes on entries (built)

As built (2026-10-17): implemented as designed below and tested (parser, voice interpreter,
store on Memory and Postgres incl. the database CHECK, every screen in
`internal/cli/notes_test.go`). Migration `003_notes.sql` is applied to the real database;
existing entries have an empty note. Notes for the voice side:
- The amount ends at the first word that isn't a number word, so a number spoken right
  after the amount still belongs to the amount: "lima puluh ribu **dua** kopi" is 50,002
  with note "kopi". Saying a word first works: "lima puluh ribu **beli** dua kopi" → 50,000,
  note "beli dua kopi". The review shows both, so such a case is visible before saving.
- Scale words are matched exactly. A misheard one right after the amount ("lima **jutah**")
  still refuses the entry instead of guessing. In the end-to-end test an English TTS voice
  produced "jutah" for "juta" and the entry was refused, while the note of the other entry
  came through ("makhan siang"); with a real Indonesian speaker this works.

Decisions confirmed by the user on 2026-10-17:
- Typed notes go **in double quotes**: `expenses 25000 "makan siang"`.
- Spoken notes keep the words **exactly as said** (no words dropped).
- Voice review command to change a note: **`c <#>`** (catatan).
- Searching notes (`find`) comes **later**, as a separate feature.

## 1. Goal

Every entry can carry a short note saying what it was for ("makan siang", "gaji oktober"),
typed or spoken, shown in every list, and editable later. Notes are optional: everything
that works today keeps working without one.

## 2. Typing a note

```
  > expenses 25000 "makan siang"
  ✓ Saved expense 25,000.00 · 2026-10-17 12:10 · makan siang

  > income 5000000 2026-10-01 09:00 "gaji oktober"
  ✓ Saved income 5,000,000.00 · 2026-10-01 09:00 · gaji oktober

  > expenses 15000
  ✓ Saved expense 15,000.00 · 2026-10-17 12:11

  > expenses 25000 makan siang
  ✗ Put the note in quotes, e.g. expenses 25000 "makan siang"
```

Grammar (replaces DESIGN §3 for income/expenses):

```
income   <amount> [<date> <time>] ["<note>"]
expenses <amount> [<date> <time>] ["<note>"]
```

Rules:
- The note is the text between the first `"` and the next `"`; it must come last.
  Anything after the closing quote is an error ("The note must be the last part").
- No closing quote: `✗ The note is missing its closing quote "`.
- A note can't contain `"` itself (there is no escaping). `""` means no note.
- Unquoted words where the date/time would be: if they don't look like a date and time,
  the error is the quote hint above (instead of today's "Invalid date").
- Spaces inside are kept (runs collapsed to one), the ends are trimmed. Max 200 characters
  ("Note is too long (max 200 characters)"). Any characters: Indonesian, emoji, numbers
  ("beli 2 kopi").
- Case is kept as typed (`"Gaji Oktober"`).

## 3. Speaking a note (`/audio`)

No quotes when speaking: the words after the spoken amount become the note, exactly as
heard (lowercase, punctuation removed, as the interpreter already reads them):

```
  ✓ #1   expense      50,000.00 · 12:10   buat makan       heard "pengeluaran lima puluh ribu buat makan."
  ✓ #2   income    5,000,000.00 · 12:11   gaji oktober     heard "pendapatan lima juta gaji oktober"
```

- "rupiah" directly after the amount is part of the amount and dropped
  ("dua puluh lima ribu rupiah makan" → note "makan").
- Safety rule change: today a word after the amount that looks like a misheard number word
  ("dua ratus **rebu**") rejects the entry. That check will apply only to the **first** word
  after the amount, so notes may contain number words
  ("pengeluaran lima puluh ribu beli kopi seribu" → note "beli kopi seribu").
- The review table gets a Note column, and `c <#>` changes a note before saving:

```
  review> c 1
  #1 expense 50,000.00    Note [buat makan] (- to remove): makan siang
```

  The review options line becomes:
  `t: set date & time of every entry · t <#>: one entry · c <#>: note · d <#>: delete`.

## 4. Showing notes

`list` (monthly), the day view (edit/delete), the trash and the voice review get a Note
column after Amount. Long notes are shortened with "…" (about 30 characters); the full
note is shown when editing. If no row in the table has a note, the column is left out, so
months without notes look exactly as before.

```
  October 2026
  ──────────────────────────────────────────────────────────────────
   #   Date               Type            Amount   Note
   1   2026-10-01 09:00   income    5,000,000.00   gaji oktober
   2   2026-10-15 19:30   expense     150,000.00   bensin
   3   2026-10-17 12:10   expense      25,000.00   makan siang
   4   2026-10-17 12:11   expense      15,000.00
  ──────────────────────────────────────────────────────────────────
```

## 5. Editing a note

`edit` gets a fourth question. It's an answer to a question, so no quotes are needed
(quotes typed anyway are removed). Enter keeps the note, `-` removes it:

```
  Type [expense]:
  Amount [25000]:
  Date & time [2026-10-17 12:10]:
  Note [makan siang] (- to remove): makan siang kantor
  Change #3:
    note         makan siang  →  makan siang kantor
  Save? [Y/n]:
```

One-line shortcut, same syntax as adding: `3 expense 25000 "makan siang kantor"`.
Without a note in the shortcut the existing note is kept (like date/time); `3 expense 25000
""` removes it.

Undo of an edit restores the old note too; the save message and `describe` lines include
the note (`expense 25,000.00 · 2026-10-17 12:10 · makan siang`).

## 6. Data and code

Migration `003_notes.sql`:

```sql
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS note TEXT NOT NULL DEFAULT '';
ALTER TABLE transactions ADD CONSTRAINT transactions_note_length CHECK (char_length(note) <= 200);
```

Existing entries get an empty note; nothing else changes.

Code changes:
- `store`: `Transaction.Note`, `NewTransaction.Note`; `AddTransaction` takes a
  `NewTransaction`; `UpdateTransaction` takes the note; validation of note length.
- `cli/parse.go`: quote-aware splitting; `AddCmd.Note` and `AddCmd.HasNote` (shortcut:
  "keep" vs "set"); quote errors.
- `cli/entries.go`: day-view input keeps case and quotes for the shortcut's note.
- `voice.Interpret`: `Draft.Note` from the words after the amount; first-word guard only.
- `cli`: note column (list, day view, trash, review), edit question, review `c <#>`.
- Help text, argument hints (`income <amount> [YYYY-MM-DD HH:MM] ["note"]`), tutorial steps
  2-4 use a note in their examples (`expenses 25000.50 "makan siang"`).

## 7. Testing

- Parser: note alone, after date + time, empty `""`, missing closing quote, text after the
  quote, unquoted words (hint), date without time, 200/201 characters, unicode, `"` count,
  case kept.
- Voice: note after word and digit amounts, "rupiah" dropped, linking words kept, number
  words inside the note allowed, misheard scale word right after the amount still rejected.
- Store (Memory + Postgres): note saved, updated, kept by undo/restore, length CHECK.
- Screens: column only when a note exists, truncation with "…", save message, edit
  keep / change / remove (`-`), shortcut with note / without note / `""`, review `c <#>`.
- End to end: synthesized WAV "pengeluaran dua puluh lima ribu makan siang" through real
  Whisper → note "makan siang".

Later: `find <text>` to search notes across all months (see PLAN "Later").
