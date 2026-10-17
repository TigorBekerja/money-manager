# Design: edit and delete saved entries (built)

As built (2026-10-17): everything below is implemented and tested (store on Memory and
Postgres, every screen flow in `internal/cli/entries_test.go`, tutorial step 6). Notes:
- `updated_at` is the version used to detect outside changes. It is forced to move forward
  on every edit (`GREATEST(clock_timestamp(), updated_at + 1 µs)`), because two edits within
  one clock tick (Windows' clock is coarse) would otherwise get the same version and an
  outside change could be overwritten by undo. A test makes rapid edits to check this.
- Edits also check the version against what the day view showed, so an entry changed in
  another window between showing and saving is not overwritten ("changed elsewhere").
- `edit 2026-10-15 09:00` / `delete 3` are rejected with a hint to pick the number in the
  day view.
- Code: `internal/cli/entries.go` (day view, delete, edit, trash, undo, day-view input
  parser), `internal/store/queries.go`, migration `002_edit.sql`.

Decisions confirmed by the user on 2026-10-17:
- Entries are picked from a **day view** that opens with `edit` / `delete` (the monthly
  `list` stays as it is).
- Navigation: `p` / `n` for the previous / next day with entries, or type a date
  (`2026-10-15`), a month (`2026-09`) or a year (`2025`).
- The day view opens on **today**.
- Deleting is **recoverable**: deleted entries stay in the database for 30 days (`trash`,
  `restore`), then are removed for good. `undo` reverses the last change right away.
- Several entries can be deleted at once; edit has a one-line shortcut.

## 1. Commands

| Command | What it does |
|---|---|
| `edit` / `edit 2026-10-15` | open the day view (today, or that day) to pick one entry to change |
| `delete` / `delete 2026-10-15` | open the day view to pick one or more entries to delete |
| `trash` | show entries deleted in the last 30 days and pick some to restore |
| `undo` | reverse the last edit, delete or restore of this session |

## 2. The day view

```
  > delete

  Saturday, 17 October 2026
  ──────────────────────────────────────────
   #   Time    Type          Amount
   1   09:00   income    5,000,000.00
   2   14:33   expense      25,000.50
  ──────────────────────────────────────────
   Income 5,000,000.00 · Expenses 25,000.50

  p: previous day · n: next day · or type a date: 2026-10-15 · 2026-09 · 2025
  Choose entry number(s) to delete, e.g. 2 or 1 3 · Enter: done
  delete> _
```

- Entries of that day, oldest first, numbered from 1, with the day's totals.
- `p` / `n`: jump to the previous / next day **that has entries** (empty days are skipped).
  At the ends: "No earlier entries." / "No later entries."
- `2026-10-15`: that day (even if it's empty: "No entries on this day.").
- `2026-09`: the first day with entries in September 2026; `2025`: the first day with
  entries in 2025. If there are none: "No entries in September 2026." and the view stays.
- Today with no entries: "No entries today. Press p to go to the last day with entries."
- Enter on an empty line (or `q`) leaves the day view. After a change the same day is shown
  again with fresh numbers, so several fixes can be made in a row.
- Invalid input shows a short error and the options again.

## 3. Delete

```
  delete> 1 2
   1   09:00   income    5,000,000.00
   2   14:33   expense      25,000.50
  Delete these 2 entries? They stay in trash for 30 days. [y/N]: y
  ✓ Deleted 2 entries. Type undo to bring them back, or find them in trash.
```

- One or more numbers separated by spaces (`2`, `1 3`); duplicates are ignored, numbers out
  of range are an error and nothing is deleted.
- Confirmation is required; Enter means **No**.
- All selected entries are deleted together (one database transaction).

## 4. Edit

Pick one number. The app asks per field; Enter keeps the current value:

```
  edit> 2
  #2  expense      25,000.50 · 2026-10-17 14:33
  Type [expense]:
  Amount [25000.50]: 27500
  Date & time [2026-10-17 14:33]: 2026-10-16
  Change #2:
    amount       25,000.50  →  27,500.00
    date & time  2026-10-17 14:33  →  2026-10-16 14:33
  Save? [Y/n]:
  ✓ Updated. It moved to Friday, 16 October 2026.
```

One-line shortcut: a number followed by the normal entry syntax skips the questions and goes
straight to the confirmation:

```
  edit> 2 expense 27500 2026-10-16 14:33
```

- Type: `income`, `expenses`/`expense`, `pendapatan`, `pengeluaran`.
- Amount: same rules as adding (`27500`, `27,500`, `27500.50`).
- Date & time: `YYYY-MM-DD HH:MM`, date only (keeps the time) or time only (keeps the date).
- In the shortcut, date and time are optional; without them the entry keeps its date/time.
- Only changed fields are listed in the confirmation; if nothing changed: "No changes."
- Invalid answers show the usual error and ask the same question again.
- Choosing two numbers: "Choose one entry to edit."

## 5. Trash and restore

```
  > trash

  Deleted entries (kept 30 days)
  ───────────────────────────────────────────────────────────────────────
   #   Deleted            Date & time        Type          Amount   Removed on
   1   2026-10-17 15:02   2026-10-17 09:00   income  5,000,000.00   2026-11-16
   2   2026-10-17 15:02   2026-10-17 14:33   expense    25,000.50   2026-11-16
  ───────────────────────────────────────────────────────────────────────
  Choose entry number(s) to restore, e.g. 1 or 1 2 · Enter: done
  trash> 2
  Restore #2 expense 25,000.50 · 2026-10-17 14:33? [Y/n]:
  ✓ Restored 1 entry.
```

- Newest deletions first; "Removed on" = deleted + 30 days.
- Restored entries keep their original id, date/time and amount.
- When the app starts, entries deleted more than 30 days ago are removed permanently.
  Nothing else removes them.

## 6. Undo

```
  > undo
  Undo: delete of 2 entries (income 5,000,000.00, expense 25,000.50)? [Y/n]:
  ✓ Undone.
```

- Reverses the last edit, delete or restore made in this session (one step; a second `undo`
  says "Nothing to undo.").
- Undo of an edit puts back the old type, amount and date/time; if the entry was changed or
  deleted elsewhere since then, undo refuses: "That entry changed since; undo skipped."
- Undo is in memory only and ends when the app closes (trash still works after that).

## 7. Data

Migration `002_edit.sql`:

```sql
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS transactions_deleted_at_idx
    ON transactions (deleted_at) WHERE deleted_at IS NOT NULL;
```

- `deleted_at` NULL = active. **Every** normal query (`list`, day view, navigation) adds
  `deleted_at IS NULL`. The monthly `list` and its totals therefore exclude deleted entries.
- `updated_at` is set on every edit; undo of an edit checks it to detect changes made
  elsewhere.
- Purge at startup: `DELETE FROM transactions WHERE deleted_at < now() - interval '30 days'`.

Store (Postgres and Memory, shared behavioural tests):

```go
var ErrNotFound = errors.New("entry not found") // missing, or deleted meanwhile

ListBetween(ctx, from, to)                      // existing; now excludes deleted rows
Nearest(ctx, t time.Time, before bool) (time.Time, bool, error) // occurred_at of the closest active entry
UpdateTransaction(ctx, id, kind, amount, occurredAt, expectUpdatedAt) (Transaction, error)
DeleteTransactions(ctx, ids) ([]Transaction, error)  // soft delete, all or nothing
RestoreTransactions(ctx, ids) ([]Transaction, error) // clears deleted_at, all or nothing
ListDeleted(ctx) ([]Transaction, error)              // newest deletion first
PurgeDeleted(ctx, before time.Time) (int, error)
```

`Transaction` gains `UpdatedAt` and `DeletedAt` (zero when unset).

## 8. CLI

- Parser: `EditCmd{Day *time.Time}`, `DeleteCmd{Day *time.Time}`, `TrashCmd{}`, `UndoCmd{}`.
- Day-view input parser (pure, tested): `p`, `n`, `YYYY-MM-DD`, `YYYY-MM`, `YYYY`, numbers,
  `<#> <type> <amount> [date time]`, empty / `q`.
- `session.lastChange` holds the undo step.
- Suggestions and `/help`: `edit`, `delete`, `trash`, `undo` (hint `[YYYY-MM-DD]`).
- Tutorial: new step "Fix a mistake" (practice `delete` and `undo` on the practice entries;
  still in memory, nothing saved). Tutorial grows to 7 steps.

Out of scope: editing/deleting by voice, bulk edit, a full change history.

## 9. Testing

- Store (Memory + Postgres): soft delete hides rows from `ListBetween`/`Nearest`; delete and
  restore all-or-nothing with `ErrNotFound`; update sets `updated_at`, rejects stale
  `expectUpdatedAt`, rejects deleted rows, keeps CHECK constraints; purge removes only rows
  older than the cutoff; `ListDeleted` order.
- Day-view input parser: table tests incl. invalid dates, `0`, out of range, duplicates.
- REPL: open on today; today empty; `p`/`n` skipping empty days and stopping at the ends;
  jump to day / month / year (with and without entries); delete one and several with
  default No; edit by questions (invalid answers re-asked, Enter keeps values, "No changes",
  moved-day note) and by shortcut; entry deleted elsewhere; trash + restore; undo of each
  action, "Nothing to undo", undo refused after an outside change; tutorial step writes
  nothing to the real store.
