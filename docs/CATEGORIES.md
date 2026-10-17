# Design: categories (built)

As built (2026-10-17): implemented as designed below and tested (store on Memory and
Postgres incl. the composite foreign key and the name CHECK, parser, executor, every
screen in `internal/cli/categories_test.go`, voice interpreter). Migration
`004_categories.sql` is applied to the real database: 7 built-ins, existing entries have no
category, row count and total unchanged. Differences from the text below:
- "Did you mean" also keeps the rest of the hint: `expenses 1000 makan` →
  `Unknown expense category "makan". Did you mean food? Type categories to see them all, or
  put a note in quotes: expenses 1000 "makan"`.
- Names can't be a type word either (income, expense(s), pendapatan, pengeluaran).
- Tab after the amount shows a menu only while a category name is partly typed
  (`expenses 5000 d` → drink, dessert); a full name or a trailing space shows none, so
  Enter always saves.
- Removing a category whose entries are all in the trash doesn't ask where to move them;
  they lose the category.
- Moving entries on remove counts as a change to them (new `updated_at`), so an edit open
  in another window, or an undo of an earlier edit of such an entry, is refused.
- Not tested with real speech: categories are recognised in the transcript text, which the
  unit tests cover; the speech engine part is unchanged.

Decisions confirmed by the user on 2026-10-17:
- Category is **optional**; no category shows as `(none)` (no built-in "other").
- Typed as a **bare word** after the amount: `expenses 25000 food "makan siang"`.
- An **unknown word is an error** with a "Did you mean" hint; new categories only via
  `category add`.
- By voice, the category word is **taken out of the note** ("makanan nasi padang" →
  food, note "nasi padang").

## 1. Goal

Every entry can belong to a category ("food", "salary"), so a month can be read as "where
did the money go". The app ships with a fixed set of built-in categories and the user can
add their own. Categories are per type: expense categories only on expenses, income
categories only on income. Entries without a category keep working exactly as today.

## 2. Built-in categories

| Type    | Name           | Also understood as (typed and spoken)              |
|---------|----------------|----------------------------------------------------|
| expense | `food`         | makanan                                            |
| expense | `drink`        | minuman                                            |
| expense | `dessert`      | pencuci mulut, hidangan penutup, makanan penutup   |
| expense | `laundry`      | londri, cucian, binatu                             |
| expense | `subscription` | langganan                                          |
| expense | `transport`    | transportasi, transpor, transpot                   |
| income  | `salary`       | gaji                                               |

- The names are English (as requested); the Indonesian words are **aliases** so voice entry
  works ("pengeluaran lima puluh ribu makanan") and typing either works
  (`expenses 50000 makanan` saves as `food`). Aliases are fixed in code for built-ins only.
- "transpot" from the request is spelled `transport`; "transpot" is kept as an alias.
- Built-ins can't be renamed or removed (the app must always have them). User categories can.
- No built-in "other": an entry without a category shows as `(none)`.

## 3. Typing a category

The category is one word right after the amount, before the date/time and the note:

```
income   <amount> [category] [YYYY-MM-DD HH:MM] ["note"]
expenses <amount> [category] [YYYY-MM-DD HH:MM] ["note"]
```

```
  > expenses 25000 food "makan siang"
  ✓ Saved expense 25,000.00 · food · 2026-10-17 12:10 · makan siang

  > income 5000000 salary 2026-10-01 09:00 "gaji oktober"
  ✓ Saved income 5,000,000.00 · salary · 2026-10-01 09:00 · gaji oktober

  > expenses 15000 minuman
  ✓ Saved expense 15,000.00 · drink · 2026-10-17 12:11

  > expenses 9000
  ✓ Saved expense 9,000.00 · 2026-10-17 12:12

  > expenses 25000 fod
  ✗ Unknown expense category "fod". Did you mean food? Type categories to see them all.

  > expenses 25000 makan
  ✗ Unknown expense category "makan". Type categories to see them all, or put a note in
    quotes: expenses 25000 "makan"

  > income 5000 food
  ✗ food is an expense category. Income categories: salary
```

Rules:
- Matching is case-insensitive; the category is stored as a reference, so `Food` and
  `makanan` both save `food`.
- The category may also come after the date and time (`expenses 25000 2026-10-17 12:10
  food`); the help shows the first order only.
- Category is **optional**: `expenses 9000` still works and has no
  category. Existing entries have no category after the migration.
- An unknown word is an error, never a silent new category: a typo like
  "fod" must not create junk. "Did you mean" uses the same edit distance as voice commands.
- `none` means no category (useful in the edit shortcut, §6).

## 4. Managing categories

```
  > categories
  Expenses                 entries
    food                        12   makanan
    drink                        4   minuman
    dessert                      0   pencuci mulut, hidangan penutup, makanan penutup
    laundry                      1   londri, cucian, binatu
    subscription                 2   langganan
    transport                    5   transportasi, transpor, transpot
    parkir                       3   added by you
  Income
    salary                       1   gaji

  category add <income|expenses> <name>   ·   category rename <name> <new>   ·   category remove <name>

  > category add expenses parkir
  ✓ Added expense category parkir.

  > category rename parkir parking
  ✓ Renamed parkir to parking (3 entries).

  > category remove parking
  3 entries use parking. Move them to (category, or Enter for none): transport
  Remove parking and move 3 entries to transport? [y/N]: y
  ✓ Removed parking; 3 entries moved to transport.
```

Rules:
- Names: one word, 1-20 characters, letters, digits and `-`, starting with a letter; stored
  lowercase. Not allowed: `none`, a name or alias that already exists for the same type
  (`category add expenses makanan` → "makanan already means food").
- The same name may exist for both types (e.g. `bonus` as income and as expense). Then
  rename/remove need the type: `category remove expenses bonus` (the error says so).
- Remove of a category in use asks where its entries go (another category of the same type,
  or none), then confirms with default No. Entries in the trash move too. This also covers
  merging two categories. Built-ins: "food is built in and can't be removed".
- `undo` stays for entries only (edit/delete/restore); category changes are not undoable,
  which is why remove confirms.
- `/tutorial` practice categories go to the tutorial's memory store only.

## 5. Showing categories

`list` gets a Category column after Type (only when a row in the month has a category, like
Note), and a breakdown per type below the totals:

```
  October 2026
  ────────────────────────────────────────────────────────────────────────
   #   Date               Type      Category           Amount   Note
   1   2026-10-01 09:00   income    salary       5,000,000.00   gaji oktober
   2   2026-10-15 19:30   expense   transport      150,000.00   bensin
   3   2026-10-17 12:10   expense   food            25,000.00   makan siang
   4   2026-10-17 12:11   expense   drink           15,000.00
   5   2026-10-17 12:12   expense                    9,000.00
  ────────────────────────────────────────────────────────────────────────
   Income                                        5,000,000.00
   Expenses                                        199,000.00
   Net                                           4,801,000.00

   Expenses by category
     transport       150,000.00   75%
     food             25,000.00   13%
     drink            15,000.00    8%
     (none)            9,000.00    5%
   Income by category
     salary        5,000,000.00  100%
```

- Breakdown sorted by amount (largest first), `(none)` last; percentages of that type's total,
  computed with integer math on cents (no floats), rounded; they may not add up to exactly 100.
- The breakdown is left out when no entry of the month has a category.
- The day view (edit/delete), the trash, and the voice review get the same Category column.
- Save message and `describe` lines: `expense 25,000.00 · food · 2026-10-17 12:10 · makan siang`.

## 6. Editing

`edit` asks for the category right after the type (the choices depend on it):

```
  Type [expense]:
  Category [food] (- to remove): drink
  Amount [25000]:
  Date & time [2026-10-17 12:10]:
  Note [makan siang] (- to remove):
  Change #3:
    category     food  →  drink
  Save? [Y/n]:
```

- Enter keeps, `-` removes, an unknown name re-asks with the list of that type's categories.
- Changing the type clears a category that doesn't fit: the question becomes
  `Category (Enter: none):` and the change list shows `category  food → (none)`.
- Shortcut, same syntax as adding: `3 expense 25000 drink "makan siang"`. Without a
  category the existing one is kept (if it fits the type); `none` removes it.
- Undo of an edit restores the old category.

## 7. Voice (`/audio`)

The first word(s) after the spoken amount are checked against that type's categories
(names and aliases; multi-word aliases like "pencuci mulut", longest match first). A match
becomes the category; the rest is the note (the category word is taken out of the note):
stays in the note)**:

```
  ✓ #1   expense   50,000.00 · food · 12:10   nasi padang    heard "pengeluaran lima puluh ribu makanan nasi padang"
  ✓ #2   income  5,000,000.00 · salary · 12:11   oktober     heard "pendapatan lima juta gaji oktober"
  ✓ #3   expense   20,000.00 · 12:12   buat makan            heard "pengeluaran dua puluh ribu buat makan"
```

- Matching: exact, or within 1 edit for words of 6+ letters (Whisper spelling slips like
  "langganan" → "langanan"). Only at that position, so note words are never taken.
- User categories work by voice too, by their name ("pengeluaran lima ribu parkir").
- The misheard-number guard still runs on the first word after the amount when it is not
  a category ("dua ratus rebu" is still refused).
- Review command `k <#>` (kategori) sets the category before saving:

```
  review> k 1
  #1 expense 50,000.00    Category [food] (- to remove): drink
```

  Options line (two lines, it's getting long):
  `t: date & time of every entry · t <#>: one entry · k <#>: category · c <#>: note`
  `d <#>: delete · r: record more · y: save all · n: discard all`

## 8. Help, suggestions, tutorial

- Help: `income <amount> [category] [YYYY-MM-DD HH:MM] ["note"]`, plus `categories` and
  `category add|rename|remove`, and an example with a category.
- Suggestions: `categories` and `category` in the command menu; after `category `:
  `add`, `rename`, `remove`; after `income 5000 ` the menu lists the income categories
  (Tab completes them) with the hint `[category] [YYYY-MM-DD HH:MM] ["note"]`. The REPL
  passes the cached category list to `Suggest`; `lineedit` stays app-agnostic.
- Tutorial (8 steps): step 2 `income 5000000 salary`, step 3
  `expenses 25000.50 food "makan siang"`, new step 4 "Categories" (try `categories`, then
  `category add expenses parkir`), step "See a month" shows the breakdown.

## 9. Data and code

Migration `004_categories.sql`:

```sql
CREATE TABLE IF NOT EXISTS categories (
    id         BIGSERIAL   PRIMARY KEY,
    kind       TEXT        NOT NULL CHECK (kind IN ('income', 'expense')),
    name       TEXT        NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,19}$' AND name <> 'none'),
    builtin    BOOLEAN     NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, name),
    UNIQUE (id, kind)                    -- target of the composite foreign key below
);

INSERT INTO categories (kind, name, builtin) VALUES
    ('expense', 'food', true), ('expense', 'drink', true), ('expense', 'dessert', true),
    ('expense', 'laundry', true), ('expense', 'subscription', true),
    ('expense', 'transport', true), ('income', 'salary', true)
ON CONFLICT (kind, name) DO NOTHING;

ALTER TABLE transactions ADD COLUMN IF NOT EXISTS category_id BIGINT;
ALTER TABLE transactions ADD CONSTRAINT transactions_category_fk
    FOREIGN KEY (category_id, kind) REFERENCES categories (id, kind);
CREATE INDEX IF NOT EXISTS transactions_category_idx ON transactions (category_id);
```

- The composite key `(category_id, kind)` lets the database itself refuse an expense
  category on an income entry. A NULL `category_id` means no category and isn't checked.
- No `ON DELETE` action: remove moves the entries first, then deletes, in one transaction.
- Existing entries get `category_id NULL`; nothing else changes.

Code changes:
- `store`: `Category{ID, Kind, Name, Builtin}`; `Transaction.CategoryID` + `Category` (name,
  from a `LEFT JOIN`); `NewTransaction.CategoryID` (0 = none) so `Values()`/undo carry it;
  `Categories`, `AddCategory`, `RenameCategory`, `RemoveCategory(id, moveTo)`,
  `CategoryCounts`; errors `ErrCategoryExists`, `ErrBuiltin`; validation that the category
  fits the kind; `Memory` seeded with the built-ins and the same behaviour.
- `store/categories.go`: built-in aliases and the name rule (shared by CLI and voice).
- `cli/parse.go`: `AddCmd.Category` (word as typed) + `HasCategory`; resolution to an id
  happens in the executor (the parser doesn't know user categories); `CategoriesCmd`,
  `CategoryCmd{Action, Kind, Name, New}`.
- `cli`: category column and breakdown (`categoryColumn`, like `noteColumn`), edit question
  and shortcut, `categories` screen, remove flow, review `k <#>`, session cache of the
  category list (refreshed after add/rename/remove).
- `voice.Interpret` takes the category list (names + aliases per kind) and fills
  `Draft.CategoryID`; `voice.Launcher` / review get it from the session.

## 10. Testing

- Parser: category alone, with date/time, with note, after the date, `none`, missing amount
  hint, quotes still last.
- Executor: built-in, alias, user category, case, wrong type, unknown + "Did you mean".
- Store (Memory + Postgres): built-ins seeded once, add/rename/remove, duplicate and invalid
  names, builtin refusal, remove moves active and trashed entries, the composite foreign key
  refuses a mismatched kind (raw SQL), `ListBetween` returns names, undo restores category.
- Screens: Category column only when used, breakdown (order, `(none)`, rounding), edit
  question/shortcut/type change, `categories` screen, remove prompt, review `k <#>`.
- Voice: name, alias, multi-word alias, fuzzy 6+ letters, user category, note after it,
  number guard still refuses "rebu".
- Real DB: apply 004 with the read-only check (row count and sum unchanged, 7 built-ins).

## 11. Later

`list 10 2026 food` (filter one category) · monthly budget per category · categories in a
yearly summary · aliases for user categories.
