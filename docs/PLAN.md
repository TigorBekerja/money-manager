# Implementation Plan (v1)

See `DESIGN.md` for the spec and `DLR.md` for decisions. Tick boxes as work lands.

## Phase 0: Environment
- [x] Postgres 18 service running locally
- [x] Settings file `%APPDATA%\money-manager-vandika\.env` created; app auto-creates the `money_manager` DB (DLR-012)
- [x] User fills in `DB_PASSWORD` in the `.env` file; `money_manager` DB auto-created on first run
- [x] `%USERPROFILE%\go\bin` is on user `PATH` (verified 2026-10-17)
- [x] `git init`, `.gitignore` (binaries, `.env`)

## Phase 1: Scaffold
- [x] Directories: `cmd/money-manager-vandika`, `internal/{cli,money,store}`, `internal/store/migrations`
- [x] Dependencies: pgx v5.11.0, x/sys v0.45.0 (last version supporting Go 1.25)
- [x] `go install ./cmd/money-manager-vandika`; `money-manager-vandika` resolves from PATH

## Phase 2: Money (`internal/money`)
- [x] `Parse(s string) (int64, error)`: digits, optional 1-2 decimals, strip `,` `_`, > 0, overflow guard
- [x] `Format(cents int64) string`: `5,000,000.00`, negatives as `-1,000.00` (for net)
- [x] Table tests for both

## Phase 3: Parser (`internal/cli/parse.go`)
- [x] `Parse(line, loc) (Command, error)` for income, expenses/expense, list, tutorial, help/?, exit/quit/q
- [x] Optional leading `/` on every command; case-insensitive; empty line returns no command
- [x] Optional `YYYY-MM-DD HH:MM` on income/expenses (local tz), both-or-neither
- [x] Error messages as in DESIGN §2.3
- [x] Table tests (DESIGN §7)

## Phase 4: Store (`internal/store`)
- [x] `Transaction`, `Kind` types
- [x] `Open(ctx, url) (*Postgres, error)` with ping and a 5s connect timeout
- [x] Embedded migrations + `Migrate(ctx)` with `schema_migrations` tracking and advisory lock
- [x] `001_init.sql` (DESIGN §5, with `occurred_at`)
- [x] `AddTransaction(ctx, kind, amount, occurredAt)`, `ListBetween(ctx, from, to)`
- [x] `Memory` store with the same behaviour (sorted by occurred_at, id)
- [x] Shared behavioural tests run against `Memory` always and Postgres when `TEST_DATABASE_URL` is set
- [x] Run the Postgres integration tests (6/6 pass against Postgres 18; no leftover schemas)

## Phase 5: REPL + rendering (`internal/cli`)
- [x] `REPL{In, Out, Store, Now, Color, Banner}`
- [x] Executor shared by REPL and tutorial; zero `At` → `Now().Truncate(time.Minute)`
- [x] Banner (version, greeting, `/tutorial` hint, DB host/port/name without credentials)
- [x] Save confirmation with date/time, list table, totals and net, empty month, help text
- [x] `list` with no args prompts for month then year, showing the current ones as defaults (`Month (1-12) [10]:`); Enter accepts, invalid input re-prompts
- [x] Exit on `/exit`, EOF, Ctrl+C (signal handler in main, prints `Bye!`)
- [x] Colors with `NO_COLOR` / non-TTY fallback
- [x] REPL tests with `store.Memory` and fixed `Now`

## Phase 6: Tutorial (`internal/cli/tutorial.go`)
- [x] Step type: title, body, optional suggested command, success message
- [x] The 6 steps from DESIGN §2.4 (steps 4-5 use the current month/year from `Now`)
- [x] Controls: Enter = next, `b` = back, `q`/`/exit` = quit tutorial; `tutorial>` prompt; "(practice, not saved)" tag
- [x] Invalid input → normal error + `Hint: try ...`
- [x] Fresh `store.Memory` per run; exit message returns to normal prompt
- [x] Tests: full run, early quit, back, invalid input, zero writes to the real store

## Phase 7: Wiring (`cmd/money-manager-vandika`)
- [x] Read `MONEY_MANAGER_DATABASE_URL` or default
- [x] Windows: enable VT processing (`console_windows.go`, build-tagged; no-op elsewhere)
- [x] Friendly startup failure, exit code 1 (verified against the real server with a wrong password)
- [x] `--version` flag

## Phase 8: Finish
- [x] `go vet ./...`, `gofmt -l .` clean, unit tests pass, Linux cross-build compiles
- [x] Scripted acceptance against the real DB: full `/tutorial` (incl. invalid input + hint, `list` prompt), then `list` on real data; 0 rows written
- [ ] Interactive check by the user in cmd / Windows Terminal: colors, box drawing, Ctrl+C, up-arrow history
- [x] CLAUDE.md and DESIGN updated to match what was built

## Phase 9: Suggestions and Tab completion (DLR-013)
- [x] `internal/lineedit`: raw-mode editor (insert, cursor keys, Home/End/Del, Backspace, Ctrl+A/E/U, history, Ctrl+C/D/Z, CRLF paste)
- [x] Suggestion menu below the input, dim ghost text, Tab / → / Enter to accept, ↑↓ to select, Esc to hide
- [x] `cli.Suggest`: command-name completion (with or without `/`), `/` lists all, argument hints
- [x] REPL uses a `LineReader`; editor on interactive terminals, plain scanner for pipes/tests
- [x] Help text and tutorial step 6 mention Tab and `/`
- [x] Tests: editor key handling (22 cases), render (ghost/menu/hint, no ghost without color), raw mode restored; suggestion rules; every suggestion parses
- [ ] Interactive check by the user in cmd / Windows Terminal (raw mode can't be exercised from piped tests)

## Phase 10: `/audio` voice entry (built, see AUDIO.md §7a)
- [x] Decisions: local whisper.cpp + large-v3-turbo q5_0; automatic entry detection with live status line; `pendapatan`/`pengeluaran`; no slang; date/time editing in review (default = recording start)
- [x] Requirements checked on this machine (AUDIO.md §2.1): all met except the downloads and mic permission check
- [x] `/setup-audio`: check first, plan GPU/CPU build + model, folder prompt, `[Y/n]`, pinned downloads with SHA-256 + size check, safe unzip, Ctrl+C stops download, writes `.env`, runs status
- [x] `/audio-status`: system, program (starts), model (complete), GPU/CUDA, microphones, mic privacy, engine test (start server, transcribe silence, GPU→CPU fallback), READY / NOT READY
- [x] `internal/speech` (`StartServer`, `Transcribe`, `Close`) and `internal/audio` (WAV encoder, mic devices, mic permission); tests incl. a fake whisper-server process
- [x] Verified on this machine: status shows GPU ✓, 2 microphones ✓, access ✓, program/model missing; setup plan shown and declined; pinned URLs return the expected sizes
- [x] User ran `/setup-audio`; `/audio-status` READY: model ready in 1.0 s on GPU, test transcription 0.3 s
- [x] `internal/voice`: Indonesian number + command interpreter, 70+ table cases, refuses misheard number words
- [x] Date/time edit parser (full, date only, time only) + tests
- [x] `store.AddTransactions` (single DB transaction) + tests on Memory and Postgres incl. rollback
- [x] `internal/audio`: waveIn capture (Windows, verified on the real mic), median-calibrated segmenter with start times, level meter, WAV encode/decode, WAV file source
- [x] `internal/lineedit`: raw `ReadKey` for listening-mode keys
- [x] `cli` listening screen with live status line (Calibrating / Ready / Recording / understanding N) and level meter
- [x] `cli` review: list all, `t` / `t <#>` / `d <#>` / `r` / `y` / `n`, `*` marker, Esc/Ctrl+C warnings with pending entries, save failure keeps entries
- [x] `/audio` in suggestions (exact match wins over `/audio-status`), help, tutorial
- [x] Opt-in end-to-end test with a WAV through the real engine (`MM_E2E_WAV`), and the installed app driven with WAV input: 3/3 correct, nothing saved, no leftover process
- [x] Voice stop: "stop" / "berhenti" on its own finishes listening (console wake verified in a real window; real Whisper recognises both)
- [ ] User test with their own voice on the real microphone

## Phase 11: edit and delete saved entries (built, see EDIT.md)
- [x] Decisions: day view for edit/delete (opens on today; p/n and typed date/month/year), recoverable delete (trash 30 days + restore), undo, multi-delete, one-line edit
- [x] Migration `002_edit.sql` (`updated_at`, `deleted_at`) + purge on startup (applied to the real DB)
- [x] Store: `Nearest`, `UpdateTransaction`, `DeleteTransactions`, `RestoreTransactions`, `ListDeleted`, `PurgeDeleted`, `ErrNotFound`; `ListBetween` excludes deleted; shared tests
- [x] Parser: `edit [date]`, `delete [date]`, `trash`, `undo`; day-view input parser
- [x] Day view: render, navigation, pick numbers
- [x] Delete (confirm, default No), edit (questions + shortcut, confirm, moved-day note), trash/restore, undo
- [x] Suggestions, `/help`, tutorial step "Fix a mistake"
- [x] Tests (EDIT.md §9), incl. outside-change and rapid-edit version checks

## Later (not v1)
Notes on transactions · categories · delete/edit last entry · yearly summary · CSV export ·
one-shot mode (`money-manager-vandika income 5000`) · Bubble Tea UI
