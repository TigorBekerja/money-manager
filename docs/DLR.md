# Decision Log Record

One entry per decision. Newest at the bottom. Don't edit accepted entries; to change one,
add a new entry that supersedes it and update the old entry's status.

Format: **Context** (why it came up), **Decision**, **Alternatives**, **Consequences**.

---

## DLR-001: Go + PostgreSQL
- Date: 2026-10-17 · Status: Accepted
- **Context:** User requirement.
- **Decision:** Go 1.25 for the app, PostgreSQL (local, v18 installed) for storage.
- **Alternatives:** SQLite would need no server, but Postgres was requested.
- **Consequences:** Postgres must be running locally for the app to start.

## DLR-002: Interactive REPL launched by command name
- Date: 2026-10-17 · Status: Accepted
- **Context:** The app should open when the user types `money-manager-vandika`, like `claude`.
- **Decision:** A single binary named `money-manager-vandika` (from
  `cmd/money-manager-vandika`), installed with `go install` into a directory on `PATH`. With no
  arguments it opens an interactive session with a banner and `>` prompt.
- **Alternatives:** One-shot subcommands only (`money-manager-vandika income 5000`). Could be
  added later on top of the same parser.
- **Consequences:** Users need `%USERPROFILE%\go\bin` on `PATH`; documented in setup.

## DLR-003: Plain line-based REPL, not a full-screen TUI
- Date: 2026-10-17 · Status: Partly superseded by DLR-013 (input is now a raw-mode line editor; output is still plain streamed lines)
- **Context:** Claude Code uses a rich TUI. v1 has four commands.
- **Decision:** `bufio` line reader + ANSI styling, no TUI framework.
- **Alternatives:** Bubble Tea / Lip Gloss (nicer input editing, history, layouts, but more
  dependencies and complexity).
- **Consequences:** No arrow-key history in v1 beyond what the Windows console provides
  natively (cmd/PowerShell do give up-arrow history for line input). Revisit if the UI grows.

## DLR-004: Money stored as int64 minor units
- Date: 2026-10-17 · Status: Accepted
- **Context:** Floats can't represent decimal amounts exactly.
- **Decision:** `int64` cents in Go, `BIGINT amount_cents` in Postgres. Input accepts up to 2
  decimals; display always shows 2 decimals with thousands separators.
- **Alternatives:** `NUMERIC(15,2)` + a decimal library (extra dependency); float64 (wrong).
- **Consequences:** All conversion goes through `internal/money`. Currencies with 3 decimals
  would need a change (out of scope).

## DLR-005: One `transactions` table with a `kind` column
- Date: 2026-10-17 · Status: Accepted
- **Context:** Two money types now (income, expense), likely more later.
- **Decision:** Single table, `kind TEXT CHECK (kind IN ('income','expense'))`, positive amounts.
- **Alternatives:** Separate tables per type; signed amounts; Postgres ENUM type (harder to
  alter than a CHECK constraint).
- **Consequences:** Adding a kind is a migration that replaces the CHECK constraint.

## DLR-006: Command is `expenses`, stored as `expense`
- Date: 2026-10-17 · Status: Accepted
- **Context:** Requested syntax is `income/expenses [amount]`.
- **Decision:** User types `expenses` (or alias `expense`); stored value is singular `expense`
  to match `income`.
- **Consequences:** Mapping lives in the parser only.

## DLR-007: Timestamp = now, filtered by local month
- Date: 2026-10-17 · Status: Superseded by DLR-010
- **Context:** v1 syntax has no date argument; list is by month and year.
- **Decision:** `created_at TIMESTAMPTZ DEFAULT now()`. `list M Y` queries
  `[first of month, first of next month)` computed in the machine's local time zone.
- **Alternatives:** `EXTRACT(MONTH/YEAR ...)` in SQL (not index-friendly, uses DB session tz).
- **Consequences:** Backdating needs a new optional date argument later.

## DLR-008: Totals computed in Go
- Date: 2026-10-17 · Status: Accepted
- **Decision:** Sum income/expenses from the rows already fetched for the list.
- **Alternatives:** Separate `SUM(...) FILTER (WHERE ...)` query.
- **Consequences:** One query per `list`; fine at personal scale (hundreds of rows per month).

## DLR-009: pgx v5, embedded SQL migrations, env-var config
- Date: 2026-10-17 · Status: Accepted
- **Decision:** `github.com/jackc/pgx/v5` driver; migrations as embedded `.sql` files applied on
  startup and tracked in `schema_migrations`; connection string from
  `MONEY_MANAGER_DATABASE_URL` with a localhost default.
- **Alternatives:** `database/sql` + `lib/pq` (maintenance mode); golang-migrate (extra
  dependency for a one-table schema); config file (more to build, env var is enough for v1).
- **Consequences:** The database must exist beforehand; tables are created automatically.

## DLR-010: Optional date + time on income/expenses (`occurred_at`)
- Date: 2026-10-17 · Status: Accepted · Supersedes DLR-007
- **Context:** User wants to enter when the money moved, e.g. `income 5000000 2026-10-17 09:00`.
- **Decision:** `income|expenses <amount> [YYYY-MM-DD HH:MM]`. Date and time are both given or
  both omitted; omitted means now (local, truncated to the minute). Stored in a new
  `occurred_at TIMESTAMPTZ NOT NULL` column; `created_at` stays as insert time for audit.
  `list` filters and sorts by `occurred_at` using the local-month half-open range from
  DLR-007. Future dates are allowed.
- **Alternatives:** Required date on every entry (more typing for the common "just now"
  case); date-only with time defaulting to 00:00 (ambiguous, user asked for date + time);
  reusing `created_at` for the user's time (loses when the row was actually entered).
- **Consequences:** Nothing is implemented yet, so this goes straight into `001_init.sql`;
  no data migration needed.

## DLR-011: Slash-style meta commands and a hands-on `/tutorial`
- Date: 2026-10-17 · Status: Accepted
- **Context:** User wants `/tutorial` so new users learn the app.
- **Decision:** A leading `/` is optional on every command; meta commands are documented as
  `/help`, `/tutorial`, `/exit` (Claude Code style) while data commands stay bare (`income`,
  `expenses`, `list`). `/tutorial` is a 6-step guided walkthrough where the user practices
  real commands; input goes through the real parser and executor against an in-memory store
  (`store.Memory`), so output and errors are real but nothing is written to Postgres.
- **Alternatives:** A static help page (no practice); practicing against the real DB and
  deleting afterwards (risky, leaves gaps in ids, needs delete support); requiring `/` on
  meta commands only (users would hit "unknown command" for `help`).
- **Consequences:** `store.Memory` must behave like Postgres (shared behavioural tests). Any
  change to commands must also update the help text and the tutorial steps.

## DLR-012: Credentials in a per-user `.env` file
- Date: 2026-10-17 · Status: Accepted · Refines DLR-009
- **Context:** User wants a `.env` to enter database credentials. The app is launched from any
  directory, so a `.env` in the working directory would only work inside the repo.
- **Decision:** Read `%APPDATA%\money-manager-vandika\.env` (`os.UserConfigDir`), written as a
  template on first run. Separate `DB_HOST/PORT/USER/PASSWORD/NAME/SSLMODE` fields, or a full
  `DATABASE_URL`. `MONEY_MANAGER_DATABASE_URL` still overrides everything. Small hand-written
  parser (no godotenv dependency); values are literal so passwords with `#`, `@`, `:` work.
  `--config` prints the file path. A missing database is created automatically on startup.
- **Alternatives:** `.env` next to the executable (lost on reinstall, `go\bin` is shared);
  `.env` in the working directory (breaks "run from anywhere"); `setx` env var only (password
  must be URL-escaped by hand, invisible to edit); OS credential manager (more code).
- **Consequences:** The password is stored in plain text in the user profile (readable by the
  same Windows user only). Process-level `DB_*` env vars are deliberately ignored.

## DLR-013: Own raw-mode line editor for suggestions and Tab completion
- Date: 2026-10-17 · Status: Accepted · Partly supersedes DLR-003
- **Context:** User wants suggestions while typing (`in` → `income`, help, tutorial) and Tab
  to complete. The console's cooked line input can't show anything until Enter is pressed.
- **Decision:** A small app-agnostic editor in `internal/lineedit` using
  `golang.org/x/term` (v0.43.0, compatible with Go 1.25) for raw mode. It draws a
  suggestion menu under the input, dim ghost text for the selected item, and argument hints;
  supports history, cursor keys, Ctrl+C/D/Z. The command list and hint rules live in
  `cli.Suggest`. Raw mode is entered per `ReadLine` and always restored, so normal output
  and the Ctrl+C signal handler behave as before outside of input. Used only when stdin and
  stdout are terminals and VT processing is available; otherwise the plain scanner is used.
- **Alternatives:** Bubble Tea `textinput` (has suggestions, but takes over the whole
  program loop; big change for one feature); `c-bata/go-prompt` (dropdown completion but
  unmaintained, flaky on Windows); `peterh/liner` / `chzyer/readline` (Tab completion but no
  inline ghost text or menu, extra dependency anyway).
- **Consequences:** Raw mode disables the console's native up-arrow history, so the editor
  implements its own (per session, in memory). Lines longer than the terminal width can
  redraw imperfectly (commands are short). New dependency `golang.org/x/term`.

## DLR-014: Voice entry with local Whisper (whisper.cpp) and review-before-save
- Date: 2026-10-17 · Status: Accepted by the user 2026-10-17 (see `docs/AUDIO.md`; not built yet)
- **Context:** User wants `/audio`: speak Indonesian entries ("income dua ratus ribu"), keep
  them in memory, review all, save to the DB only after agreeing, then clear memory.
- **Decision (accepted):** Whisper large-v3-turbo (q5_0) via a prebuilt whisper.cpp
  `whisper-server` (CUDA build; RTX 3060 present) started by the app on localhost; mic
  capture through the Windows waveIn API (no cgo); energy-based VAD; a pure Indonesian
  transcript interpreter; pending drafts in memory; batch save in one DB transaction.
  Speech engine behind a `Transcriber` interface so a cloud API can be swapped in via `.env`.
- **Alternatives:** OpenAI / Groq / Google / Azure speech APIs (accurate, but paid, need
  internet, and send financial audio to a third party); whisper.cpp Go bindings (cgo + CUDA
  toolchain); `whisper-cli` per utterance (reloads the model every time).
- **Consequences:** ~550 MB model download and a whisper.cpp install outside the repo;
  Windows-only mic capture at first; new `.env` keys for the speech engine.

## DLR-015: Voice entry details: automatic segmentation, command words, date/time in review
- Date: 2026-10-17 · Status: Accepted (not built yet)
- **Context:** User answers to the open questions in AUDIO.md.
- **Decision:** Entries end automatically after ~0.8 s of silence, with a live status line
  (Ready for entry #N / Recording / Understanding). Command words: `income`/`pendapatan`
  and `expenses`/`expense`/`pengeluaran` (plus fuzzy spellings); no other synonyms, no slang
  amounts, no "rebu". Dates are not spoken: each entry defaults to the moment its recording
  started, and the review lets the user set date/time for every entry (`t`) or one (`t <#>`),
  accepting full `YYYY-MM-DD HH:MM`, date only, or time only; Enter keeps the default.
- **Alternatives:** push-to-talk (more reliable when noisy, slower to use); spoken dates
  ("kemarin", "tanggal 15"), harder to parse reliably, review editing covers it.
- **Consequences:** The review needs a date/time edit parser that allows partial input,
  unlike the main command grammar's both-or-neither rule (different context: there is a
  default to keep the missing half from).

## DLR-016: `/setup-audio` and `/audio-status`; pinned, verified downloads
- Date: 2026-10-17 · Status: Accepted
- **Context:** User wants a command that installs the speech program (checking first whether
  it's already there) and a command that shows whether audio-to-text can be used.
- **Decision:** In-app `/setup-audio` and `/audio-status` (package `internal/speech`).
  Downloads are **pinned** to whisper.cpp v1.9.4 / release b5130 (GPU build
  `whisper-cublas-12.4.0-bin-x64.zip`, or the CPU build without a CUDA 12.4-capable NVIDIA GPU)
  and the model at a fixed Hugging Face revision; each file must match its pinned size and
  SHA-256 or it's deleted. Default folder `%LOCALAPPDATA%\money-manager-vandika\whisper`
  (the user can choose another). Paths go into `.env` via `config.SetValues`, which keeps all
  other lines. `/audio-status` includes a real engine test (start server, transcribe
  silence, GPU then CPU fallback). Ctrl+C during a download cancels it, not the app.
- **Alternatives:** always fetch "latest" release (unreviewed changes, release asset names
  move between tags); a `--setup-audio` command-line flag (the user asked for an in-app
  command); hashing the 547 MB model on every status (slow; size check + verify-on-download
  instead); D:\Tools\whisper as default (machine-specific).
- **Consequences:** Upgrading whisper.cpp means updating URL, size and digest together in
  `internal/speech/assets.go`. The newest build (b5454, 11 days old) was skipped on purpose.

## DLR-017: Voice entry as built: safety rules and tuning
- Date: 2026-10-17 · Status: Accepted
- **Context:** Decisions made while building `/audio` and testing it with real Whisper output.
- **Decision:**
  - Ignore words after a complete amount, but refuse the entry if any looks like a misheard
    number word (within 1 edit of ribu/juta/ratus/puluh/...): a wrong amount is worse than a
    retry.
  - Fuzzy command-word limits scale with word length (1 / 2 for 7+ / 3 for 10+ letters).
  - Segmenter noise floor = median of the first 0.5 s; threshold 2.5× noise.
  - whisper-server in a kill-on-close job object; one server per `/audio`, reused by `r`.
  - `MONEY_MANAGER_AUDIO_INPUT` (WAV instead of mic) for development and end-to-end tests.
  - When a typed command is complete and also a prefix of others (`/audio`, `/audio-status`),
    the exact match is suggested first so Enter runs what was typed.
- **Alternatives:** strict "no trailing words" (rejects natural notes like "buat makan");
  mean-based calibration (a start-up click inflated it); 3.5× threshold (risked missing quiet
  speech in this room).
- **Consequences:** Thresholds may need tuning after real use; they are constants in
  `audio.DefaultSegmenterConfig` and `segmenter.threshold`.

## DLR-018: Spoken "stop" / "berhenti" ends listening
- Date: 2026-10-17 · Status: Accepted
- **Context:** User wants to stop listening by voice, not only with Enter.
- **Decision:** An utterance consisting only of a stop word (stop, setop, berhenti, berenti,
  or within one edit of berhenti; leading fillers allowed) behaves like Enter. To release the
  key read that is blocked in raw mode, the app writes a private-use character (U+E000,
  `lineedit.WakeRune`) into its own console input with `WriteConsoleInputW`; `ReadKey`
  returns "wake" for it and `ReadLine` ignores it. Without a console, the user is asked to
  press Enter.
- **Alternatives:** polling the console for key events with a timeout (key-up/focus events
  make `ReadConsole` block anyway); a background key reader shared with the review prompt
  (two readers on one input); cancelling the read (not possible for synchronous console reads).
- **Consequences:** "stop" can't be used as an entry; the stop word must be said on its own.

## DLR-019: Edit and delete saved entries
- Date: 2026-10-17 · Status: Accepted (see `docs/EDIT.md`; not built yet)
- **Context:** User wants to fix or remove entries already saved.
- **Decision:** `edit` / `delete` open a day view (today by default, or a given date) listing
  that day's entries with numbers; `p`/`n` jump to the previous/next day with entries, or the
  user types a date, month or year. Delete takes one or more numbers and confirms (default
  No). Edit takes one number and asks per field (Enter keeps), or a one-line
  `<#> <type> <amount> [date time]`, then confirms. Deletes are soft (`deleted_at`): `trash`
  shows the last 30 days and restores; rows deleted over 30 days ago are purged at startup.
  `undo` reverses the last edit/delete/restore of the session. `updated_at` guards undo
  against outside changes.
- **Alternatives:** numbers from the monthly `list` (rejected by the user in favour of a day
  view); database ids; permanent delete with or without undo.
- **Consequences:** every normal query must filter `deleted_at IS NULL`; the monthly list
  and totals exclude deleted entries; purging depends on the app being started.

## DLR-020: `updated_at` as an always-increasing version
- Date: 2026-10-17 · Status: Accepted
- **Context:** Undo and edit use `updated_at` to detect changes made from another window.
  Testing showed two edits within one clock tick can get the same timestamp (Windows' clock
  is coarse), so an outside change went unnoticed and undo overwrote it.
- **Decision:** every edit sets `updated_at = GREATEST(clock_timestamp(), updated_at + 1 µs)`
  (Memory store: the same rule), so the version always changes. Edits pass the version the
  day view showed; undo passes the version its own edit produced.
- **Alternatives:** a separate integer `version` column (cleaner, but another migration and
  column for the same guarantee).
- **Consequences:** `updated_at` can be a microsecond ahead of the wall clock after rapid
  edits; it's only used for comparisons and audit.
