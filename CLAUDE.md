# CLAUDE.md

Guidance for Claude Code when working in this repository.

## What this is

`money-manager-vandika` is a personal money manager CLI written in Go with PostgreSQL storage.
Typing `money-manager-vandika` in any terminal (cmd, PowerShell, Windows Terminal) opens an
interactive session, similar to how `claude` opens Claude Code. Inside the session the user types
commands such as `income 5000000 2026-10-17 09:00`, `expenses 25000`, `list 10 2026` and
`/tutorial`.

Docs:
- `docs/DESIGN.md`: architecture, schema, command grammar, UX mockups
- `docs/PLAN.md`: implementation phases and checklist (keep it updated as work lands)
- `docs/DLR.md`: decision log; add an entry whenever a non-obvious decision is made or reversed
- `docs/AUDIO.md`: `/audio` voice entry design (built)
- `docs/EDIT.md`: edit/delete saved entries, trash, undo (built)

## Commands

```powershell
go build ./...                                   # compile everything
go test ./...                                    # unit tests (no DB needed)
$env:TEST_DATABASE_URL="postgres://..."; go test ./internal/store/...   # DB integration tests
go run ./cmd/money-manager-vandika               # run without installing
go install ./cmd/money-manager-vandika           # install to %USERPROFILE%\go\bin
go vet ./...
gofmt -l .                                       # must print nothing
```

The binary name comes from the directory name `cmd/money-manager-vandika`. Do not rename it.

Smoke-testing the installed binary: it talks to the user's **real** database. Pipe in only
`/tutorial` steps and read-only commands (`list`, `/help`, `/exit`), never `income`/`expenses`.
Real-DB writes are covered by `go test` with `TEST_DATABASE_URL` (throwaway schemas).
`/audio` against the real DB: always finish with `n` + `y` (discard). To feed speech without
a microphone, set `MONEY_MANAGER_AUDIO_INPUT=<16 kHz mono 16-bit WAV>`; with piped stdin, keys
are read as lines, so pace the input (e.g. a .NET Process writing `/audio`, waiting ~20 s,
then an empty line for Enter).

Opt-in tests: `MM_TEST_MIC=1` (records 1 s from the real mic, prints only the level),
`MM_E2E_WAV` + `MM_E2E_EXPECT` + `WHISPER_SERVER` + `WHISPER_MODEL` (real engine end to end,
see `internal/voice/e2e_test.go`). A synthesized test WAV can be made with
`System.Speech` (English voices only on this machine; they still work for the pipeline).

Windows PowerShell 5.1 prefixes piped stdin with a UTF-8 BOM; the REPL strips it, but if you
ever see `Unknown command "﻿..."`, that's the cause.

## Layout

```
cmd/money-manager-vandika/   main.go: load config, connect DB, run migrations, start REPL
internal/cli/                REPL loop, command parsing, executor, rendering, /tutorial
internal/config/             .env settings file + env var → database URL
internal/lineedit/           raw-mode line editor: history, suggestion menu, ghost text, Tab
internal/speech/             whisper.cpp: /audio-status checks, /setup-audio pinned downloads, whisper-server client
internal/audio/              mic capture (waveIn), speech segmenter, WAV encode/decode, mic devices + privacy check
internal/voice/              Indonesian transcript interpreter, listening session, engine launcher (/audio)
internal/money/              amount parsing and formatting (int64 minor units)
internal/store/              Transaction type, PostgreSQL (pgx v5), migrations, in-memory store
internal/store/migrations/   *.sql files, applied in filename order on startup
docs/                        DESIGN, PLAN, DLR
```

## Rules

- **Never use floats for money.** Amounts are `int64` minor units (cents) in Go and `BIGINT`
  in Postgres. Parse with `money.Parse`, print with `money.Format`.
- Transaction type is stored as `'income'` or `'expense'`. The user-facing command is
  `expenses` (with `expense` accepted as an alias); map it in the parser, not in the store.
- All SQL lives in `internal/store`. `internal/cli` depends on a small `Store` interface,
  implemented by `store.Postgres` and `store.Memory` (used by tests and the tutorial).
- `occurred_at` is when the money moved (user-given `YYYY-MM-DD HH:MM`, or now truncated to the
  minute, local time zone). `created_at` is insert time, audit only. List, filter and sort
  by `occurred_at`.
- Month filtering uses a half-open range `[first day of month, first day of next month)` in the
  user's local time zone, computed in Go and passed as parameters. Never use
  `EXTRACT(MONTH ...)` in the WHERE clause (it can't use the index).
- Get "now" from the REPL's injected `Now func() time.Time`, never `time.Now()` directly, so
  tests are deterministic.
- `/tutorial` must never write to Postgres: it runs the real parser and executor against a
  fresh `store.Memory`. When adding or changing a command, update the help text and the
  tutorial steps too.
- A leading `/` is optional on every command. Write meta commands as `/help`, `/tutorial`,
  `/exit` in user-facing text; data commands stay bare (`income`, `expenses`, `list`).
- Always use query parameters (`$1`, `$2`), never string-built SQL.
- Deletes are soft (`deleted_at`). Every normal query (list, totals, day view, navigation)
  must filter `deleted_at IS NULL`; only the trash queries look at deleted rows. Rows in the
  trash longer than `store.TrashDays` (30) are purged at startup.
- Edits must pass the entry's `UpdatedAt` as the expected version (`ErrChanged` if someone
  else edited it); undo passes the version its own edit produced. Never bypass this check.
- Errors shown to the user are short and actionable. A bad command must never crash the REPL.
- Keep dependencies minimal. Current: `github.com/jackc/pgx/v5`, `golang.org/x/sys` (Windows
  console VT mode), `golang.org/x/term` (raw mode for the line editor). Ask before adding others.
- Speech downloads are pinned in `internal/speech/assets.go` (URL + size + SHA-256). Never
  switch to "latest"; to upgrade, take all three values for one release from the GitHub
  release API (`digest` field) / Hugging Face (`lfs.oid`). Tests never hit the network: they
  use httptest servers and the test binary as a fake `whisper-server` (`FAKE_WHISPER` env).
- Suggestions: the command list, menu descriptions and argument hints live in
  `internal/cli/suggest.go` (`commands`). When adding or changing a command, update it there
  as well as in the parser, help text and tutorial. `internal/lineedit` must stay
  app-agnostic (no imports from `internal/cli`).
- `lineedit.WakeRune` (U+E000) is injected into the console to release a pending `ReadKey`
  when voice input hears "stop" (`cmd/.../console_windows.go`). Keep it ignored in
  `ReadLine`. Real-console check: `TestWakeConsole` (opt-in, needs its own console window).
- Write Go source with the Write/Edit tools, not Python/sed with escapes: `\n`, `\u...` and
  `\x..` inside inserted Go strings have repeatedly been turned into real characters.
- The module targets Go 1.25.5 (the installed toolchain). When adding or upgrading
  dependencies, set `$env:GOTOOLCHAIN='local'` first; otherwise `go get` silently bumps the
  `go` directive and downloads a newer toolchain if a dependency needs one (latest
  `golang.org/x/sys` requires Go 1.26). Pick an older dependency version instead.
- Target platform is Windows first; keep code portable (Windows-only code behind build tags).

## Configuration

Credentials live in a `.env` file in the user config dir, not the repo:
`%APPDATA%\money-manager-vandika\.env` (print it with `money-manager-vandika --config`).
The app writes a template there on first run. `internal/config` resolves the connection in
this order:

1. `MONEY_MANAGER_DATABASE_URL` environment variable (full URL)
2. `DATABASE_URL` in the `.env` file
3. `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE` in the `.env` file,
   with defaults `localhost`, `5432`, `postgres`, (empty), `money_manager`, `disable`

`.env` values are literal (no inline comments, optional surrounding quotes); the URL is built
with `net/url` so special characters in passwords are escaped. `DB_*` keys are read from the
file only, never from the process environment (too generic, would collide).

If the database doesn't exist, startup creates it (via the `postgres` maintenance DB);
tables are created by migrations. `TEST_DATABASE_URL` enables the Postgres integration tests
(each test uses a throwaway schema).
