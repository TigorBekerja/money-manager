# Design: money-manager-vandika

## 1. Goal

A personal money manager that runs as an interactive terminal app. The user types
`money-manager-vandika` in cmd and lands in a session with a prompt, the way `claude` opens
Claude Code. v1 tracks two kinds of money movement, **income** and **expenses**, stores them
in PostgreSQL, and shows a monthly list with totals.

### In scope (v1)
- Launch from any terminal by name
- `income <amount> [<date> <time>]` and `expenses <amount> [<date> <time>]` save a
  transaction at the given date and time, or now if omitted
  (e.g. `income 5000000 2026-10-17 09:00`)
- `list <month> <year>` shows that month's transactions, total income, total expenses, net
- `/tutorial`: guided, hands-on walkthrough of the app (nothing is saved)
- `/help`, `/exit`

### Out of scope (v1, candidates for later)
- Notes/descriptions, categories, editing or deleting entries
- Multiple currencies, multiple users
- Full-screen TUI (Bubble Tea), charts, export

## 2. User experience

### 2.1 Launch

```
C:\Users\vandi> money-manager-vandika

  ╭────────────────────────────────────────────────╮
  │  Money Manager                          v0.1.0 │
  │                                                │
  │  Hi Vandika! Type /help to see commands.       │
  │  New here? Type /tutorial for a quick guide.   │
  │  db: localhost:5432/money_manager              │
  ╰────────────────────────────────────────────────╯

  > _
```

### 2.2 Session

```
  > income 5000000 2026-10-01 09:00
  ✓ Saved income 5,000,000.00 · 2026-10-01 09:00

  > expenses 150000 2026-10-15 19:30
  ✓ Saved expense 150,000.00 · 2026-10-15 19:30

  > expenses 25000.50
  ✓ Saved expense 25,000.50 · 2026-10-17 14:33          ← no date given: now

  > list 10 2026

  October 2026
  ────────────────────────────────────────────────
   #   Date               Type        Amount
   1   2026-10-01 09:00   income    5,000,000.00
   2   2026-10-15 19:30   expense     150,000.00
   3   2026-10-17 14:33   expense      25,000.50
  ────────────────────────────────────────────────
   Income                           5,000,000.00
   Expenses                           175,000.50
   Net                              4,824,999.50

  > list
  Month (1-12) [10]: 9
  Year [2026]:                                           ← Enter keeps the default

  September 2026
  No transactions.

  > list
  Month (1-12) [10]:                                     ← Enter, Enter: current month
  Year [2026]:

  October 2026
  ...

  > /exit
  Bye!
```

Colors (when the terminal supports them): income/positive net in green, expense/negative net
in red, headings bold, hints dim. With `NO_COLOR` set or a non-terminal stdout, plain text.

### 2.3 Errors (REPL keeps running)

```
  > income
  ✗ Missing amount. Usage: income <amount> [YYYY-MM-DD HH:MM]   e.g. income 50000

  > expenses -20
  ✗ Amount must be a positive number, e.g. 25000 or 25000.50

  > income 5000 2026-02-30 09:00
  ✗ Invalid date "2026-02-30". Use YYYY-MM-DD HH:MM, e.g. 2026-10-17 09:00

  > income 5000 2026-10-17
  ✗ Missing time. Use YYYY-MM-DD HH:MM, e.g. 2026-10-17 09:00

  > income 5000 2026-10-17 9pm
  ✗ Invalid time "9pm". Use 24-hour HH:MM, e.g. 21:00

  > list 13 2026
  ✗ Month must be 1-12. Usage: list <month> <year>

  > spend 100
  ✗ Unknown command "spend". Type /help to see commands.
```

If the database is unreachable at startup, print the error, the URL being used (password
masked) and how to fix it (start Postgres, create the DB, or set
`MONEY_MANAGER_DATABASE_URL`), then exit with code 1.

### 2.4 Tutorial (`/tutorial`)

A step-by-step guide. Each step explains one thing and asks the user to try it. What the
user types is run through the **real parser** and executed against an **in-memory store**,
so they see real output and real error messages, and the final `list` step shows the entries
they made during the tutorial. Nothing touches Postgres.

Controls on every step: type the suggested command to try it, `Enter` on an empty line to
continue, `b` to go back, `q` to leave the tutorial.

```
  > /tutorial

  Tutorial · 1/6 · Welcome
  ────────────────────────────────────────────────
  Money Manager keeps track of money coming in (income) and
  money going out (expenses), and shows a summary for any month.

  This is practice mode: nothing you type here is saved.

  Enter: next · b: back · q: quit tutorial
  tutorial> _
```

```
  Tutorial · 2/6 · Record income
  ────────────────────────────────────────────────
  When you receive money, type income followed by the amount.
  Commas and up to 2 decimals are fine: 5000000, 5,000,000, 5000000.50

  Try it:  income 5000000

  tutorial> income 5000000
  ✓ Saved income 5,000,000.00 · 2026-10-17 14:32   (practice, not saved)
  Nice! Without a date, the current date and time are used.

  Enter: next · b: back · q: quit tutorial
```

Steps:

| # | Title             | Teaches                                                 | Try it                               |
|---|-------------------|---------------------------------------------------------|--------------------------------------|
| 1 | Welcome           | what the app does, practice mode                        | (none)                               |
| 2 | Record income     | `income <amount>`, amount formats, default = now        | `income 5000000`                     |
| 3 | Record an expense | `expenses <amount>` (`expense` also works)              | `expenses 25000.50`                  |
| 4 | Add date and time | `YYYY-MM-DD HH:MM`, 24-hour, both parts required        | `expenses 150000 2026-10-15 19:30`   |
| 5 | See a month       | `list <month> <year>`; `list` + Enter, Enter = current month; totals & net | `list <current month> <current year>`|
| 6 | You're set        | `/help`, `/tutorial`, `/exit`; Tab completion, `/` menu, up-arrow history | (none)            |

If the user types something that isn't valid, they see the normal error plus
`Hint: try <suggested command>`. If they type a valid but different command (e.g.
`income 100` on the expense step), it still runs in practice mode and the step continues.
After step 6 (or `q`), the app prints `Tutorial finished. Back to your real data.` and
returns to the normal `>` prompt.

### 2.5 Suggestions and Tab completion

While typing a command name, matching commands appear in a menu below the input and the
selected one is shown as dim ghost text after the cursor (Claude Code style):

```
  > in█come
      › income     record money coming in

  > e█xpenses
      › expenses   record money going out
        /exit      quit

  > /█
      › income     record money coming in
        expenses   record money going out
        list       show a month (Enter = current)
        /tutorial  guided walkthrough
        /help      show all commands
        /exit      quit
```

Keys:

| Key                   | Effect                                                                 |
|-----------------------|------------------------------------------------------------------------|
| `Tab`, `→` at end     | accept the selected suggestion (`in` → `income `)                      |
| `↑` / `↓`             | move through the menu when it's open; otherwise previous/next history  |
| `Enter`               | menu open: accept; runs it if the command needs no arguments (`he` → `/help` runs), otherwise keeps editing (`in` → `income `). Menu closed: submit the line |
| `Esc`                 | hide the menu until the next keystroke                                 |
| `←` `→` `Home` `End` `Del` `Backspace` `Ctrl+A/E/U` | normal line editing                      |
| `Ctrl+C`              | quit (`Bye!`)                                                          |
| `Ctrl+D` / `Ctrl+Z` on an empty line | quit                                                    |

Once a command name is complete, a dim argument hint shows what comes next:
`income ` → `<amount> [YYYY-MM-DD HH:MM]`, `income 5000 ` → `[YYYY-MM-DD HH:MM]`,
`list ` → `[<month> <year>]`. Hints are informational; Tab doesn't insert them.

Suggestions work at the `>` prompt and the `tutorial>` prompt; the `Month`/`Year`
questions use plain editing without suggestions or history. Ghost text and hints are only
drawn with color on (otherwise they'd look like typed text); the menu is always shown.
When stdin or stdout isn't an interactive terminal (pipes, tests), the app falls back to
plain line reading with no suggestions.

## 3. Command grammar

Commands are case-insensitive; arguments are whitespace-separated. A leading `/` is optional
on every command (`/list 10 2026` = `list 10 2026`). Docs and hints write the meta commands
with a slash (`/help`, `/tutorial`, `/exit`), the same style as Claude Code.

```
income    <amount> [<date> <time>]
expenses  <amount> [<date> <time>]   (alias: expense)
list      [<month> <year>]           (no args: prompt for month, then year; Enter = current)
tutorial                             (shown as /tutorial)
help                                 (shown as /help; alias: ?)
exit                                 (shown as /exit; aliases: quit, q; also Ctrl+C / EOF)
```

`<amount>`:
- digits with optional `.` and up to 2 decimals: `50000`, `50000.5`, `50000.50`
- thousands separators `,` and `_` are accepted and stripped: `5,000,000`
- must be > 0 and at most 999,999,999,999,999.99 (fits int64 minor units comfortably)
- no signs, no exponents, no currency symbols

`<date> <time>`:
- `YYYY-MM-DD HH:MM`, 24-hour, e.g. `2026-10-17 09:00`; single-digit hour allowed (`9:00`)
- both parts or neither; omitted means the current local date and time (seconds dropped)
- must be a real calendar date (`2026-02-30` is rejected), year 1970-9999
- interpreted in the machine's local time zone; future dates are allowed (planned entries)

`<month>`: integer 1-12. `<year>`: 4-digit integer, 1970-9999.

## 4. Architecture

```
          ┌──────────────────────────────────────────────┐
 stdin ──▶│ cli.REPL                                     │──▶ stdout
          │   read line → cli.Parse → Command            │
          │   execute Command against Store              │
          │   render result (cli/render.go)              │
          │                                              │
          │   /tutorial → cli.Tutorial                   │
          │     same Parse + execute + render,           │
          │     against store.Memory (never Postgres)    │
          └──────────────┬───────────────────────────────┘
                         │ Store interface
          ┌──────────────▼──────────────┐   ┌───────────────────────────┐
          │ store.Postgres (pgx v5)     │   │ store.Memory              │
          │   Migrate · Add · List      │   │   tutorial + tests        │
          └──────────────┬──────────────┘   └───────────────────────────┘
                         ▼
                    PostgreSQL
```

Packages:

| Package                     | Responsibility                                                      |
|-----------------------------|---------------------------------------------------------------------|
| `cmd/money-manager-vandika` | wiring: config, connect, migrate, enable VT mode, run REPL          |
| `internal/cli`              | REPL loop, parser (pure), executor, renderer, prompts, tutorial, command suggestions (`Suggest`) |
| `internal/lineedit`         | raw-mode line editor: keys, history, suggestion menu, ghost text; app-agnostic |
| `internal/config`           | `.env` settings file + env var → database URL                       |
| `internal/money`            | `Parse(string) (int64, error)`, `Format(int64) string`              |
| `internal/store`            | `Transaction` type, Postgres implementation, migrations, `Memory`   |

Key types:

```go
// internal/store
type Kind string
const (
    Income  Kind = "income"
    Expense Kind = "expense"
)

type Transaction struct {
    ID         int64
    Kind       Kind
    Amount     int64     // minor units (cents)
    OccurredAt time.Time // when the money moved (user-given or now)
    CreatedAt  time.Time // when the row was inserted (audit only)
}

// internal/cli
type Store interface {
    AddTransaction(ctx context.Context, kind store.Kind, amount int64, occurredAt time.Time) (store.Transaction, error)
    ListBetween(ctx context.Context, from, to time.Time) ([]store.Transaction, error)
}

type Command interface{ isCommand() }
type AddCmd      struct{ Kind store.Kind; Amount int64; At time.Time } // zero At = now
type ListCmd     struct{ Month time.Month; Year int; Prompt bool }
type TutorialCmd struct{}
type HelpCmd     struct{}
type ExitCmd     struct{}
```

The REPL holds a `Now func() time.Time` so "now" is injectable in tests; the executor fills a
zero `At` with `Now().Truncate(time.Minute)`.

The executor (`execute(ctx, Store, Command) → result`) and the renderer are shared by the
REPL and the tutorial. The tutorial is just a sequence of steps, each with text, an optional
suggested command, and a success message, driving the same executor with a `store.Memory`.

Totals are computed in Go from the rows returned by `ListBetween`, so the list and the
totals are always consistent and cost one query.

## 5. Database

```sql
-- internal/store/migrations/001_init.sql
CREATE TABLE IF NOT EXISTS transactions (
    id           BIGSERIAL   PRIMARY KEY,
    kind         TEXT        NOT NULL CHECK (kind IN ('income', 'expense')),
    amount_cents BIGINT      NOT NULL CHECK (amount_cents > 0),
    occurred_at  TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS transactions_occurred_at_idx ON transactions (occurred_at);
```

`schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ)` is created by
`Migrate` itself before any migration file runs.

Queries:

```sql
-- AddTransaction
INSERT INTO transactions (kind, amount_cents, occurred_at) VALUES ($1, $2, $3)
RETURNING id, kind, amount_cents, occurred_at, created_at;

-- ListBetween: $1 = first instant of month, $2 = first instant of next month (local tz)
SELECT id, kind, amount_cents, occurred_at, created_at
FROM transactions
WHERE occurred_at >= $1 AND occurred_at < $2
ORDER BY occurred_at, id;
```

Migrations: SQL files embedded with `//go:embed migrations/*.sql`, applied in filename order
inside one transaction that first takes `pg_advisory_xact_lock` (so two terminals starting at
once can't race); each applied filename is recorded in `schema_migrations` and skipped
next time.

## 6. Launching by name

`go install ./cmd/money-manager-vandika` builds `money-manager-vandika.exe` into
`%USERPROFILE%\go\bin` (already on this machine's `PATH`). Then the command works in any new
cmd/PowerShell window.

### Credentials

Because the app runs from any directory, its settings live in the user config dir:
`%APPDATA%\money-manager-vandika\.env` (`money-manager-vandika --config` prints the path).
First run writes this template:

```
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=
DB_NAME=money_manager
DB_SSLMODE=disable
# DATABASE_URL=postgres://user:password@localhost:5432/money_manager?sslmode=disable
```

Resolution order: `MONEY_MANAGER_DATABASE_URL` env var → `DATABASE_URL` in the file →
`DB_*` fields (missing ones fall back to the defaults above). Values are literal, so
passwords need no URL escaping. On connection failure the error names the `.env` path to
edit (or the env var, if that's where the URL came from). If the server reports the database
doesn't exist (SQLSTATE `3D000`), the app creates it with the same credentials and continues.

On Windows, `main` enables `ENABLE_VIRTUAL_TERMINAL_PROCESSING` on the console so ANSI
colors and box characters render in legacy cmd as well as Windows Terminal; if that fails,
output falls back to plain text.

## 7. Testing

- `internal/money`: table tests for parse (valid, decimals, separators, zero, negative,
  too many decimals, overflow, garbage) and format.
- `internal/cli` parser: table tests incl. date/time (with/without, `9:00`, missing time,
  invalid date like `2026-02-30`, invalid time like `24:00`/`9pm`, extra args), slash prefix.
- `internal/cli` REPL: drive an `io.Reader` script against `store.Memory` with a fixed `Now`
  and assert on output (save with/without date, list, totals, errors, exit, EOF).
- `internal/cli` tutorial: scripted run through all steps (try, invalid input + hint, back,
  quit early); assert the practiced entries appear in step 5 and that the REPL's real store
  received zero writes.
- `internal/store`: integration tests against `TEST_DATABASE_URL` (skipped if unset): migrate
  twice is idempotent, insert + list, month boundaries on `occurred_at` (last minute of month
  vs first minute of next), CHECK constraints reject bad rows. `store.Memory` gets the same
  behavioural tests so it can't drift from Postgres.
- Manual acceptance: install, open a fresh cmd, run `/tutorial`, then the session from §2.2.
