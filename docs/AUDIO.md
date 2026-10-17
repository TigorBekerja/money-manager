# Design: `/audio` voice entry (built)

Decisions confirmed by the user on 2026-10-17: local Whisper, automatic entry detection
with on-screen recording status, `pendapatan`/`pengeluaran` as command words, no slang
amounts, and date/time editing for every entry after listening (default = moment recorded).

## 1. Goal

`/audio` switches the session to listening mode. The user speaks entries in Indonesian,
e.g. *"income dua ratus ribu"*, and the app turns each one into an entry
(`income 200000`). Entries are kept **in memory only** while recording. When the user
finishes, the app lists **every** recorded entry, lets the user set the date and time of
each one, and only after the user agrees saves them to Postgres and removes them from memory.

## 2. Speech model: Whisper large-v3-turbo, local, via whisper.cpp

| | |
|---|---|
| Model | OpenAI Whisper **large-v3-turbo** (open weights, MIT), quantized `ggml-large-v3-turbo-q5_0.bin` (547 MB) |
| Runtime | [whisper.cpp](https://github.com/ggml-org/whisper.cpp) **v1.9.4** (release b5130) prebuilt Windows **CUDA 12.4** build: `whisper-cublas-12.4.0-bin-x64.zip` (643 MB); CPU build `whisper-bin-x64.zip` (8 MB) when there is no suitable NVIDIA GPU |
| Speed (expected) | short utterance (2-4 s): well under 1 s on the RTX 3060; ~1-3 s CPU-only |
| Cost / privacy | free; audio never leaves the machine |
| Accuracy aid | `language=id` plus a vocabulary prompt: "income, expenses, pendapatan, pengeluaran, ribu, juta, ratus" |

Cloud APIs (OpenAI `gpt-4o-mini-transcribe`, Groq, Google, Azure) were considered and
rejected for now: paid, need internet, send financial audio to a third party. The engine is
behind a `Transcriber` interface, so one can be added later via `.env`.

### 2.1 Requirements (checked on this machine 2026-10-17)

| Requirement | Needed | This machine |
|---|---|---|
| OS | Windows 10/11 64-bit | Windows 11 Home ✓ |
| CPU | x64 with AVX2 (for the CPU fallback) | Ryzen 5 5600H, 6 cores ✓ |
| GPU (optional, for speed) | NVIDIA, ~1.5 GB free VRAM for turbo q5_0 | RTX 3060 Laptop, 6 GB ✓ |
| NVIDIA driver | supports CUDA 12.4 (driver 551+) | 592.27, CUDA 13.1 ✓ |
| CUDA Toolkit | **not needed**; the CUDA zip ships the runtime DLLs | — |
| Microsoft Visual C++ 2015-2022 Redistributable x64 | yes | 14.51 installed ✓ |
| RAM | ~2 GB free (CPU mode loads the model into RAM) | 15.3 GB ✓ |
| Disk | ~2.4 GB during install (zip + unpacked + model), less after | C: 24.7 GB free ✓ (default folder is on C:) |
| Microphone | any; Windows default input is used | Realtek HD Audio present ✓ |
| Mic permission | Settings → Privacy & security → Microphone → "Let desktop apps access your microphone" = On | allowed ✓ (checked by `/audio-status`) |
| Python / ffmpeg / cgo | **not needed** | — |

### 2.2 Setup and status commands (built)

**`/setup-audio`**
1. Checks first: system, speech program (exists and starts: `--help`), model (exists, full
   size), GPU (nvidia-smi, CUDA >= 12.4), microphones, microphone privacy setting.
2. If everything is there: "already installed, nothing to download". If the files exist
   but `.env` lacks their paths: saves the paths.
3. Otherwise lists what it will download (GPU or CPU build of the program, and/or the
   model) with sizes and disk space needed, asks for the install folder
   (default `%LOCALAPPDATA%\money-manager-vandika\whisper`) and confirmation `[Y/n]`.
4. Downloads with a progress bar; every file is checked against its pinned SHA-256 and
   size and deleted if it doesn't match. The program zip is unpacked (only the folder that
   contains `whisper-server.exe`, with protection against unsafe paths) and removed.
   Ctrl+C stops the download (the unfinished file is deleted) without quitting the app.
5. Writes `WHISPER_SERVER` / `WHISPER_MODEL` into `.env` (other lines untouched), then
   runs the full status check.

**`/audio-status`**: the same checks plus an **engine test**: starts `whisper-server`
with the model (GPU first, CPU fallback), transcribes one second of silence, and reports
load time and GPU/CPU. Ends with `Audio-to-text: READY` or `NOT READY` + the next step.

Downloads are pinned (URL + size + SHA-256 in `internal/speech/assets.go`), not "latest":
the model from a fixed Hugging Face revision, the program from release b5130. To upgrade,
update all three fields together from the GitHub release API.

## 3. User experience

### 3.1 Listening (automatic entry detection)

One live status line at the bottom is redrawn in place and always shows what the app is
doing; finished entries are printed above it and stay on screen.

```
  > /audio
  Starting speech model... ready (GPU).
  Say one entry, then pause. Example: "income dua ratus ribu"
  Enter: finish and review · Esc: cancel

  ✓ #1  income    200,000.00 · 14:32   heard "income dua ratus ribu"
  ✓ #2  expense    55,000.00 · 14:33   heard "pengeluaran lima puluh lima ribu"
  ✗     heard "beli kopi": no income/expenses word. Try "pengeluaran dua puluh ribu"

  ● Ready for entry #3 · speak now        ▂▃▁        ← live status line
```

Status line states:

| State | Shown |
|---|---|
| waiting for speech | `● Ready for entry #N · speak now` + mic level meter |
| speech detected | `◉ Recording entry #N...` (red) + level meter |
| after ~2.5 s silence | `… Understanding entry #N...` |
| result | permanent line above (`✓ #N ...` or `✗ ...`), then back to `● Ready for entry #N+1` |

Rules:
- An entry ends automatically after ~2.5 s of silence (max 30 s per entry; was 0.8 s / 15 s, see DLR-023).
  Shorter pauses (thinking before the note) stay inside the entry. A "stop" or a next entry
  said within that time lands in the same transcript; `voice.Split` separates them.
- The entry's date/time is **the moment recording of that entry started**.
- Background noise is ignored using a noise-floor calibration in the first ~0.5 s
  ("Calibrating microphone, stay quiet..."). Speech that doesn't parse is shown as `✗` and
  not added.
- While one entry is being understood, the app keeps listening, so the next entry can be
  spoken right away.

### 3.2 Review, date/time editing, save

After Enter, the app stops listening and shows **all** pending entries:

```
  Recorded entries (NOT saved yet)
  ───────────────────────────────────────────────────────────────────────────────
   #   Heard                               Type        Amount   Date & time
   1   income dua ratus ribu               income   200,000.00  2026-10-17 14:32
   2   pengeluaran lima puluh lima ribu    expense   55,000.00  2026-10-17 14:33
  ───────────────────────────────────────────────────────────────────────────────
   Income 200,000.00 · Expenses 55,000.00
   Date & time is when each entry was recorded, unless you change it.

  t: set date & time of every entry · t <#>: one entry · d <#>: delete
  r: record more · y: save all · n: discard all
  review> t

  #1 income 200,000.00    Date & time [2026-10-17 14:32]: 2026-10-15 09:00
  #2 expense 55,000.00    Date & time [2026-10-17 14:33]:              ← Enter keeps it

   #   Heard                               Type        Amount   Date & time
   1   income dua ratus ribu               income   200,000.00  2026-10-15 09:00 *
   2   pengeluaran lima puluh lima ribu    expense   55,000.00  2026-10-17 14:33
   * changed by you
  ...
  review> y
  ✓ Saved 2 entries. Recordings cleared from memory.
```

Date & time input at the `Date & time [...]` prompt:

| Typed | Result (recorded = 2026-10-17 14:32) |
|---|---|
| (Enter) | unchanged: 2026-10-17 14:32 |
| `2026-10-15 09:00` | 2026-10-15 09:00 |
| `2026-10-15` | date only: 2026-10-15 14:32 (keeps recorded time) |
| `09:00` | time only: 2026-10-17 09:00 (keeps recorded date) |
| invalid (`2026-02-30`, `25:00`) | same error messages as the normal commands, asks again |

Review rules:
- The list is shown again after every change, so what the user agrees to is always visible.
- `y` saves all pending entries in **one database transaction** (all or nothing), then
  clears them from memory. If saving fails, nothing is saved and entries stay pending.
- `n` asks once more ("Discard 2 entries? y/n") before clearing.
- `r` resumes listening; numbering continues.
- `/exit`, Ctrl+C or Esc with pending entries: warn and offer to review first.
- Pending entries are lost if the app crashes (memory only, as requested).

## 4. Understanding Indonesian speech (`internal/voice`)

### 4.1 Command word

| Kind | Accepted words (case-insensitive) |
|---|---|
| income | `income`, `pendapatan` |
| expense | `expenses`, `expense`, `pengeluaran` |

Plus fuzzy matching (edit distance ≤ 2) for spellings Whisper produces with `language=id`,
e.g. "inkam", "inkom", "ekspenses", "pengeluran". The command word must be the first word
(after trimming filler like "eh", "oke").

### 4.2 Amount

| Spoken / transcribed | Amount |
|---|---|
| dua ratus ribu | 200,000 |
| seratus dua puluh lima ribu | 125,000 |
| sebelas ribu lima ratus | 11,500 |
| dua juta lima ratus ribu | 2,500,000 |
| satu setengah juta / dua koma lima juta | 1,500,000 / 2,500,000 |
| setengah juta | 500,000 |
| 200.000 / Rp200.000 / Rp 200.000,00 | 200,000 (Indonesian: `.` thousands, `,` decimal) |
| 200 ribu / 2,5 juta | 200,000 / 2,500,000 |

Vocabulary: nol, satu/se-, dua ... sembilan, sepuluh, sebelas, *-belas*, *-puluh*,
*-ratus*, *-ribu*, *-juta*, *-miliar*/milyar, triliun, setengah, koma, Rp/rupiah.
No slang amounts (cepek, goceng, ...) and no colloquial "rebu" for now.
Ambiguous or empty amounts are rejected with a message, never guessed.

### 4.3 Date/time
Not spoken. Defaults to the moment recording of the entry started; changed in review (§3.2).

## 5. Architecture

```
 microphone
     │  16 kHz mono 16-bit PCM
     ▼
 audio.Recorder  (Windows waveIn API via golang.org/x/sys/windows, no cgo)
     │  frames + level
     ▼
 audio.Segmenter (noise-floor calibration, speech start, end after ~2.5 s silence, max 30 s)
     │  Utterance{WAV, StartedAt}            ──► status line: Ready / Recording / Understanding
     ▼
 stt.Transcriber ──► whisper-server.exe on 127.0.0.1 (started by the app on first /audio)
     │  "income dua ratus ribu"
     ▼
 voice.Interpret ──► Draft{Kind, Amount, At=StartedAt, Heard} or error
     │
     ▼
 cli audio mode: pending []Draft (memory) → review (t / d / r / y / n)
     → store.AddTransactions (one DB transaction) → pending cleared
```

| Package | Responsibility |
|---|---|
| `internal/audio` | mic capture (Windows build tag), segmenter, level meter, WAV encoding |
| `internal/speech` | whisper.cpp: status checks, pinned downloads + install, `StartServer` / `Transcribe` (built) |
| `internal/voice` | Indonesian transcript → Draft (pure, no I/O) |
| `internal/cli/audio.go` | listening screen + status line, review and date/time editing |
| `internal/store` | new `AddTransactions(ctx, []NewTransaction)`: all or nothing |
| `internal/lineedit` | new raw `ReadKey` so Enter/Esc work while audio is captured |

```go
// internal/speech (*Server from StartServer has this shape; built)
type Transcriber interface {
    Transcribe(ctx context.Context, wav []byte) (string, error)
    Close() error
}

// internal/voice
type Draft struct {
    Kind     store.Kind
    Amount   int64     // cents
    At       time.Time // recording start, or the user's edit
    Recorded time.Time // original recording start (for the "* changed" marker)
    Heard    string    // raw transcript, shown in the review
}
func Interpret(text string, at time.Time) (Draft, error)
```

### whisper.cpp integration
- On the first `/audio` of a session the app starts
  `whisper-server.exe -m <model> -l id --host 127.0.0.1 --port <free port>`, waits for it to
  be ready (model load, a few seconds), and stops it when the app exits.
- Each utterance: `POST /inference` (multipart WAV, `response_format=json`, `language=id`,
  `prompt=...`).
- If the CUDA build fails to start, fall back to CPU (`--no-gpu`) and say so.
- Not using Go bindings (need cgo + CUDA toolchain) or `whisper-cli` per utterance (reloads
  the model every time).

## 6. Settings (`.env`)

```
# Voice input (/audio)
# written by /setup-audio
WHISPER_SERVER=C:\Users\vandi\AppData\Local\money-manager-vandika\whisper\whisper-server.exe
WHISPER_MODEL=C:\Users\vandi\AppData\Local\money-manager-vandika\whisper\ggml-large-v3-turbo-q5_0.bin
# WHISPER_GPU=auto          auto | off
# AUDIO_SILENCE_MS=2500     pause that ends an entry (not built)
```

## 7. Testing
- `voice.Interpret`: large table of phrases from §4 (word and digit forms, fuzzy command
  words, rejections such as slang and missing amount).
- Date/time edit parsing (§3.2 table) incl. invalid input.
- Segmenter: synthetic PCM (silence / tone / silence) → boundaries and `StartedAt`.
- `stt/whisperlocal`: fake HTTP server; opt-in end-to-end test with real recorded WAV
  samples when `WHISPER_MODEL` is set.
- Listening + review flow: fake recorder yielding canned utterances + fake transcriber;
  assert status lines, nothing reaches the store before `y`, everything after, memory
  cleared, edited dates saved, unedited ones equal recording start.
- `store.AddTransactions`: all-or-nothing on Memory and Postgres.
- Manual: real mic, quiet and noisy room.

## 7a. As built (2026-10-17)

- **Verified end to end** with a synthesized WAV (English TTS voice reading Indonesian) through
  the real segmenter, whisper-server on the RTX 3060 and the interpreter: 3/3 entries correct
  with correct start times, 2.9 s total; then through the installed app's `/audio` screen
  (review, date-only edit, discard). Microphone capture verified (1 s, real device).
- **Not yet verified:** a real Indonesian voice on the real microphone (needs the user).
- **Amount safety:** words after a complete amount are ignored, *unless* one looks like a
  misheard number word (`rebu`, `juto`, within 1 edit of ribu/juta/ratus/...): then the
  entry is refused instead of guessed (`dua ratus rebu` would otherwise become Rp200).
- **Fuzzy command words:** edit distance 1 for short variants, 2 for 7+ letters, 3 for 10+
  letters (`pengeluaran` heard as "pencheliaran" from an English accent still works;
  pendapatan/pengeluaran are 6+ edits apart).
- **Segmenter:** background noise = median RMS of the first 0.5 s (immune to a start-up pop),
  speech threshold 2.5× noise (this laptop's room noise measured RMS ~1200).
- **Esc with entries recorded:** asks "Stop and discard N entries? [y/N]"; the default keeps
  them and opens the review. Ctrl+C at the review asks before quitting.
- **whisper-server** runs in a Windows job object, so it dies with the app even on a crash.
  It starts once per `/audio` (about 1 s) and is reused by `r` (record more).
- **Voice stop:** saying only "stop" or "berhenti" (also "setop", "berenti", one-letter
  slips such as "birhenti"; fillers like "oke" allowed before it) works like Enter: entries
  already spoken are finished, then the review opens. The stop word inside an entry
  ("income stop ...") doesn't count. Because a key read is pending in raw mode, the app
  injects a private-use character (U+E000) into its own console input
  (`WriteConsoleInputW`) to release it; the line editor ignores that character everywhere
  else. Without a console (pipes) it asks "Press Enter to review." Verified in a real console
  window and with real Whisper ("stop." and "birhenti." both recognised).
- **Dev/test input:** `MONEY_MANAGER_AUDIO_INPUT=<16 kHz mono WAV>` replaces the microphone.

## 8. Risks

| Risk | Mitigation |
|---|---|
| Mis-heard amount saved | mandatory review of all entries, raw transcript shown next to each |
| "income" spelled oddly by Whisper | fuzzy matching + `pendapatan` + vocabulary prompt |
| `200.000` read as 200 | Indonesian separators in the speech interpreter |
| Noise creates entries | calibration; non-matching speech shown as `✗` and dropped |
| CUDA build won't start | automatic CPU fallback |
| Pending entries lost on crash | accepted; warn on exit with pending entries |
