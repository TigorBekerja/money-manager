package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"money-manager/internal/money"
	"money-manager/internal/store"
)

// change is the last edit, delete or restore of this session, kept for undo.
type change struct {
	kind   string              // "edit", "delete" or "restore"
	before store.Transaction   // edit: the old values
	after  store.Transaction   // edit: the saved new values (UpdatedAt guards the undo)
	rows   []store.Transaction // delete/restore: the affected entries
}

type pickMode int

const (
	pickEdit pickMode = iota
	pickDelete
)

func (m pickMode) String() string {
	if m == pickEdit {
		return "edit"
	}
	return "delete"
}

const dayLayout = "Monday, 2 January 2006"

func dayStart(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// describe renders an entry as "expense 25,000.50 · food · 2026-10-17 14:33 · makan siang".
func (s *session) describe(tx store.Transaction) string {
	d := fmt.Sprintf("%s %s · ", tx.Kind, money.Format(tx.Amount))
	if tx.Category != "" {
		d += tx.Category + " · "
	}
	d += tx.OccurredAt.In(s.loc()).Format(timeLayout)
	if tx.Note != "" {
		d += " · " + tx.Note
	}
	return d
}

// dayView shows one day's entries and lets the user move between days and pick entries.
func (s *session) dayView(ctx context.Context, mode pickMode, start time.Time) error {
	loc := s.loc()
	today := dayStart(s.now(), loc)
	day := today
	if !start.IsZero() {
		day = dayStart(start, loc)
	}
	var prevDay, nextDay neighbour
	for {
		rows, err := s.store.ListBetween(ctx, day, day.AddDate(0, 0, 1))
		if err != nil {
			renderError(s.out, s.st, fmt.Errorf("Couldn't load entries: %v", err))
			return nil
		}
		s.renderDay(day, rows, today)
		// The nearest days with entries, shown next to p / n and used by them.
		var err1, err2 error
		prevDay.at, prevDay.ok, err1 = s.store.Nearest(ctx, day, true)
		nextDay.at, nextDay.ok, err2 = s.store.Nearest(ctx, day.AddDate(0, 0, 1), false)
		if err := errors.Join(err1, err2); err != nil {
			renderError(s.out, s.st, fmt.Errorf("Couldn't load entries: %v", err))
			return nil
		}
		s.dayOptions(mode, day, prevDay, nextDay)

	input:
		for {
			line, err := s.readLine(mode.String()+"> ", false)
			if err != nil {
				return err
			}
			in, err := parseDayInput(line, loc)
			if err != nil {
				renderError(s.out, s.st, err)
				continue
			}
			switch in.action {
			case actDone:
				return nil
			case actPrev, actNext:
				at, ok := prevDay.at, prevDay.ok
				if in.action == actNext {
					at, ok = nextDay.at, nextDay.ok
				}
				if !ok {
					if in.action == actPrev {
						fmt.Fprintln(s.out, indent+"No earlier entries.")
					} else {
						fmt.Fprintln(s.out, indent+"No later entries.")
					}
					continue
				}
				day = dayStart(at, loc)
				break input
			case actDay:
				day = in.day
				break input
			case actMonth, actYear:
				from := time.Date(in.year, in.month, 1, 0, 0, 0, 0, loc)
				to, label := from.AddDate(0, 1, 0), from.Format("January 2006")
				if in.action == actYear {
					to, label = from.AddDate(1, 0, 0), strconv.Itoa(in.year)
				}
				at, ok, err := s.store.Nearest(ctx, from, false)
				if err != nil {
					renderError(s.out, s.st, err)
					continue
				}
				if !ok || !at.Before(to) {
					fmt.Fprintf(s.out, "%sNo entries in %s.\n", indent, label)
					continue
				}
				day = dayStart(at, loc)
				break input
			case actPick, actEditLine:
				if err := checkPick(in.nums, len(rows)); err != nil {
					renderError(s.out, s.st, err)
					continue
				}
				switch {
				case mode == pickDelete && in.action == actEditLine:
					renderError(s.out, s.st, errors.New("To change an entry, use edit."))
					continue
				case mode == pickDelete:
					var picked []store.Transaction
					for _, n := range in.nums {
						picked = append(picked, rows[n-1])
					}
					if err := s.deleteEntries(ctx, in.nums, picked); err != nil {
						return err
					}
				case len(in.nums) != 1:
					renderError(s.out, s.st, errors.New("Choose one entry to edit."))
					continue
				default:
					var shortcut *AddCmd
					if in.action == actEditLine {
						shortcut = &in.edit
					}
					if err := s.editEntry(ctx, in.nums[0], rows[in.nums[0]-1], shortcut); err != nil {
						return err
					}
				}
				break input
			}
		}
	}
}

func checkPick(nums []int, n int) error {
	if n == 0 {
		return errors.New("There are no entries on this day. Use p, n or type a date.")
	}
	for _, x := range nums {
		if x > n {
			return fmt.Errorf("Choose entry numbers from 1 to %d.", n)
		}
	}
	return nil
}

func (s *session) renderDay(day time.Time, rows []store.Transaction, today time.Time) {
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+s.st.bold(day.Format(dayLayout)))
	if len(rows) == 0 {
		if day.Equal(today) {
			fmt.Fprintln(s.out, indent+s.st.dim("No entries today. Press p to go to the last day with entries."))
		} else {
			fmt.Fprintln(s.out, indent+s.st.dim("No entries on this day."))
		}
		return
	}
	var income, expenses int64
	numW, amtW := len(strconv.Itoa(len(rows))), len("Amount")
	for _, tx := range rows {
		amtW = max(amtW, len(money.Format(tx.Amount)))
		if tx.Kind == store.Income {
			income += tx.Amount
		} else {
			expenses += tx.Amount
		}
	}
	sep := "   "
	cc := newCategoryColumn(rows, sep)
	nc := newNoteColumn(rows, sep)
	rule := indent + s.st.dim(strings.Repeat("─", 1+numW+3*len(sep)+5+7+cc.width()+amtW+nc.width()))
	fmt.Fprintln(s.out, rule)
	fmt.Fprintln(s.out, indent+" "+s.st.dim(padRight("#", numW)+sep+"Time "+sep+padRight("Type", 7)+sep+
		cc.header()+padLeft("Amount", amtW)+nc.header()))
	for i, tx := range rows {
		fmt.Fprintln(s.out, indent+" "+padRight(strconv.Itoa(i+1), numW)+sep+tx.OccurredAt.In(s.loc()).Format("15:04")+sep+
			s.st.kind(tx.Kind, padRight(string(tx.Kind), 7))+sep+cc.cell(tx.Category)+padLeft(money.Format(tx.Amount), amtW)+nc.cell(tx.Note))
	}
	fmt.Fprintln(s.out, rule)
	fmt.Fprintf(s.out, "%s Income %s · Expenses %s\n", indent, s.st.green(money.Format(income)), s.st.red(money.Format(expenses)))
}

// neighbour is the nearest earlier or later day with entries, if any.
type neighbour struct {
	at time.Time
	ok bool
}

// label names the neighbour's day: "Fri 16 Oct", with the year if it isn't day's year.
func (n neighbour) label(day time.Time, loc *time.Location) string {
	t := n.at.In(loc)
	if t.Year() != day.Year() {
		return t.Format("Mon 2 Jan 2006")
	}
	return t.Format("Mon 2 Jan")
}

func (s *session) dayOptions(mode pickMode, day time.Time, prev, next neighbour) {
	loc := s.loc()
	p, n := s.st.dim("no earlier entries"), s.st.dim("no later entries")
	if prev.ok {
		p = "p: previous day, " + s.st.bold(prev.label(day, loc))
	}
	if next.ok {
		n = "n: next day, " + s.st.bold(next.label(day, loc))
	}
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+p+" · "+n+" · or type a date: 2026-10-15 · 2026-09 · 2025")
	if mode == pickDelete {
		fmt.Fprintln(s.out, indent+"Choose entry number(s) to delete, e.g. 2 or 1 3 · Enter: done")
	} else {
		fmt.Fprintln(s.out, indent+"Choose an entry number to edit, e.g. 2 (or 2 expense 27500 food 2026-10-16 14:33) · Enter: done")
	}
}

func (s *session) deleteEntries(ctx context.Context, nums []int, rows []store.Transaction) error {
	for i, tx := range rows {
		fmt.Fprintf(s.out, "%s#%d  %s\n", indent, nums[i], s.describe(tx))
	}
	q := "Delete this entry? It stays in trash for 30 days. [y/N]: "
	if len(rows) > 1 {
		q = fmt.Sprintf("Delete these %d entries? They stay in trash for 30 days. [y/N]: ", len(rows))
	}
	answer, err := s.readLine(q, false)
	if err != nil {
		return err
	}
	if !isYes(answer) {
		fmt.Fprintln(s.out, indent+"Nothing deleted.")
		return nil
	}
	ids := make([]int64, len(rows))
	for i, tx := range rows {
		ids[i] = tx.ID
	}
	deleted, err := s.store.DeleteTransactions(ctx, ids)
	switch {
	case errors.Is(err, store.ErrNotFound):
		renderError(s.out, s.st, errors.New("One of these entries was changed or deleted elsewhere. Nothing was deleted."))
		return nil
	case err != nil:
		renderError(s.out, s.st, fmt.Errorf("Couldn't delete: %v", err))
		return nil
	}
	s.lastChange = &change{kind: "delete", rows: deleted}
	fmt.Fprintf(s.out, "%s%s Deleted %s. Type undo to bring it back, or find it in trash.\n", indent, s.st.green("✓"), entries(len(deleted)))
	return nil
}

func parseKind(s string) (store.Kind, error) {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "/")) {
	case "income", "pendapatan":
		return store.Income, nil
	case "expense", "expenses", "pengeluaran":
		return store.Expense, nil
	}
	return "", errors.New("Type income or expenses (pendapatan / pengeluaran also work).")
}

// plainAmount renders cents for an input default: "25000.50", "150000".
func plainAmount(c int64) string {
	if c%100 == 0 {
		return strconv.FormatInt(c/100, 10)
	}
	return fmt.Sprintf("%d.%02d", c/100, c%100)
}

func (s *session) editEntry(ctx context.Context, n int, row store.Transaction, shortcut *AddCmd) error {
	loc := s.loc()
	kind, amount, at, note := row.Kind, row.Amount, row.OccurredAt.In(loc), row.Note
	cat := store.Category{ID: row.CategoryID, Kind: row.Kind, Name: row.Category}
	fmt.Fprintf(s.out, "%s#%d  %s\n", indent, n, s.describe(row))

	if shortcut != nil {
		kind, amount = shortcut.Kind, shortcut.Amount
		if !shortcut.At.IsZero() {
			at = shortcut.At
		}
		if shortcut.HasNote {
			note = shortcut.Note
		}
		switch {
		case shortcut.Category != "":
			c, err := s.categoryOf(ctx, *shortcut)
			if err != nil {
				renderError(s.out, s.st, err)
				return nil
			}
			cat = c
		case kind != row.Kind:
			cat = store.Category{} // it belongs to the other type
		}
	} else {
		var err error
		if kind, err = askField(s, fmt.Sprintf("Type [%s]: ", row.Kind), kind, parseKind); err != nil {
			return err
		}
		if kind != row.Kind {
			cat = store.Category{}
		}
		if cat, err = s.askCategory(ctx, kind, cat); err != nil {
			return err
		}
		if amount, err = askField(s, fmt.Sprintf("Amount [%s]: ", plainAmount(row.Amount)), amount, money.Parse); err != nil {
			return err
		}
		parseAt := func(line string) (time.Time, error) { return ParseEditTime(line, at) }
		if at, err = askField(s, fmt.Sprintf("Date & time [%s]: ", at.Format(timeLayout)), at, parseAt); err != nil {
			return err
		}
		if note, err = s.askNote(note); err != nil {
			return err
		}
	}

	type diff struct{ name, from, to string }
	var diffs []diff
	if kind != row.Kind {
		diffs = append(diffs, diff{"type", string(row.Kind), string(kind)})
	}
	if cat.ID != row.CategoryID {
		diffs = append(diffs, diff{"category", orNone(row.Category), orNone(cat.Name)})
	}
	if amount != row.Amount {
		diffs = append(diffs, diff{"amount", money.Format(row.Amount), money.Format(amount)})
	}
	if !at.Equal(row.OccurredAt) {
		diffs = append(diffs, diff{"date & time", row.OccurredAt.In(loc).Format(timeLayout), at.Format(timeLayout)})
	}
	if note != row.Note {
		diffs = append(diffs, diff{"note", orNone(row.Note), orNone(note)})
	}
	if len(diffs) == 0 {
		fmt.Fprintln(s.out, indent+"No changes.")
		return nil
	}
	fmt.Fprintf(s.out, "%sChange #%d:\n", indent, n)
	for _, d := range diffs {
		fmt.Fprintf(s.out, "%s  %s  %s  →  %s\n", indent, padRight(d.name, 11), d.from, d.to)
	}
	answer, err := s.readLine("Save? [Y/n]: ", false)
	if err != nil {
		return err
	}
	if a := strings.TrimSpace(answer); a != "" && !isYes(a) {
		fmt.Fprintln(s.out, indent+"Nothing changed.")
		return nil
	}

	up, err := s.store.UpdateTransaction(ctx, row.ID,
		store.NewTransaction{Kind: kind, Amount: amount, OccurredAt: at, Note: note, CategoryID: cat.ID}, row.UpdatedAt)
	switch {
	case errors.Is(err, store.ErrCategoryKind):
		s.reloadCategories(ctx)
		renderError(s.out, s.st, fmt.Errorf("The category %s was removed elsewhere. Nothing was saved.", cat.Name))
		return nil
	case errors.Is(err, store.ErrChanged):
		renderError(s.out, s.st, errors.New("That entry was changed elsewhere since it was shown. Nothing was saved."))
		return nil
	case errors.Is(err, store.ErrNotFound):
		renderError(s.out, s.st, errors.New("That entry was deleted elsewhere. Nothing was saved."))
		return nil
	case err != nil:
		renderError(s.out, s.st, fmt.Errorf("Couldn't save: %v", err))
		return nil
	}
	s.lastChange = &change{kind: "edit", before: row, after: up}
	msg := indent + s.st.green("✓") + " Updated."
	if newDay := dayStart(at, loc); !newDay.Equal(dayStart(row.OccurredAt, loc)) {
		msg += " It moved to " + newDay.Format(dayLayout) + "."
	}
	fmt.Fprintln(s.out, msg)
	return nil
}

func orNone(n string) string {
	if n == "" {
		return "(none)"
	}
	return n
}

// askNote asks for a note: Enter keeps current, "-" removes it. Quotes are optional here.
func (s *session) askNote(current string) (string, error) {
	prompt := "Note (Enter: none): "
	if current != "" {
		prompt = fmt.Sprintf("Note [%s] (- to remove): ", current)
	}
	return askField(s, prompt, current, func(line string) (string, error) {
		if line == "-" {
			return "", nil
		}
		if strings.HasPrefix(line, `"`) && strings.HasSuffix(line, `"`) && len(line) >= 2 {
			line = line[1 : len(line)-1]
		}
		if strings.Contains(line, `"`) {
			return "", errors.New(`A note can't contain the " character.`)
		}
		return cleanNote(line)
	})
}

// askField asks until the answer parses; Enter keeps def.
func askField[T any](s *session, prompt string, def T, parse func(string) (T, error)) (T, error) {
	for {
		line, err := s.readLine(prompt, false)
		if err != nil {
			return def, err
		}
		if line == "" {
			return def, nil
		}
		v, err := parse(line)
		if err == nil {
			return v, nil
		}
		renderError(s.out, s.st, err)
	}
}

func (s *session) trashView(ctx context.Context) error {
	restoredAny := false
	for {
		rows, err := s.store.ListDeleted(ctx)
		if err != nil {
			renderError(s.out, s.st, fmt.Errorf("Couldn't load the trash: %v", err))
			return nil
		}
		fmt.Fprintln(s.out)
		if len(rows) == 0 {
			if restoredAny {
				fmt.Fprintln(s.out, indent+"The trash is now empty.")
			} else {
				fmt.Fprintln(s.out, indent+"The trash is empty.")
			}
			return nil
		}
		s.renderTrash(rows)

		for {
			line, err := s.readLine("trash> ", false)
			if err != nil {
				return err
			}
			if l := strings.ToLower(line); l == "" || l == "q" {
				return nil
			}
			nums, err := parseNumbers(line)
			if err == nil {
				err = checkPick(nums, len(rows))
			}
			if err != nil {
				renderError(s.out, s.st, errors.New("Choose entry number(s) to restore, e.g. 1 or 1 2, or press Enter."))
				continue
			}
			var picked []store.Transaction
			for _, n := range nums {
				picked = append(picked, rows[n-1])
			}
			done, err := s.restoreEntries(ctx, nums, picked)
			if err != nil {
				return err
			}
			restoredAny = restoredAny || done
			break
		}
	}
}

func (s *session) renderTrash(rows []store.Transaction) {
	loc := s.loc()
	numW, amtW := len(strconv.Itoa(len(rows))), len("Amount")
	for _, tx := range rows {
		amtW = max(amtW, len(money.Format(tx.Amount)))
	}
	sep := "   "
	cc := newCategoryColumn(rows, sep)
	nc := newNoteColumn(rows, sep)
	head := padRight("#", numW) + sep + padRight("Deleted", 16) + sep + padRight("Date & time", 16) + sep +
		padRight("Type", 7) + sep + cc.header() + padLeft("Amount", amtW) + sep + "Removed on"
	rule := indent + s.st.dim(strings.Repeat("─", 1+len(head)+nc.width()))
	head += nc.header()
	fmt.Fprintln(s.out, indent+s.st.bold(fmt.Sprintf("Deleted entries (kept %d days)", store.TrashDays)))
	fmt.Fprintln(s.out, rule)
	fmt.Fprintln(s.out, indent+" "+s.st.dim(head))
	for i, tx := range rows {
		fmt.Fprintln(s.out, indent+" "+padRight(strconv.Itoa(i+1), numW)+sep+
			tx.DeletedAt.In(loc).Format(timeLayout)+sep+tx.OccurredAt.In(loc).Format(timeLayout)+sep+
			s.st.kind(tx.Kind, padRight(string(tx.Kind), 7))+sep+cc.cell(tx.Category)+padLeft(money.Format(tx.Amount), amtW)+sep+
			padRight(tx.DeletedAt.In(loc).AddDate(0, 0, store.TrashDays).Format("2006-01-02"), len("Removed on"))+
			nc.cell(tx.Note))
	}
	fmt.Fprintln(s.out, rule)
	fmt.Fprintln(s.out, indent+"Choose entry number(s) to restore, e.g. 1 or 1 2 · Enter: done")
}

func (s *session) restoreEntries(ctx context.Context, nums []int, rows []store.Transaction) (bool, error) {
	q := fmt.Sprintf("Restore #%d %s? [Y/n]: ", nums[0], s.describe(rows[0]))
	if len(rows) > 1 {
		for i, tx := range rows {
			fmt.Fprintf(s.out, "%s#%d  %s\n", indent, nums[i], s.describe(tx))
		}
		q = fmt.Sprintf("Restore these %d entries? [Y/n]: ", len(rows))
	}
	answer, err := s.readLine(q, false)
	if err != nil {
		return false, err
	}
	if a := strings.TrimSpace(answer); a != "" && !isYes(a) {
		fmt.Fprintln(s.out, indent+"Nothing restored.")
		return false, nil
	}
	ids := make([]int64, len(rows))
	for i, tx := range rows {
		ids[i] = tx.ID
	}
	restored, err := s.store.RestoreTransactions(ctx, ids)
	switch {
	case errors.Is(err, store.ErrNotFound):
		renderError(s.out, s.st, errors.New("One of these entries was restored or removed elsewhere. Nothing was restored."))
		return false, nil
	case err != nil:
		renderError(s.out, s.st, fmt.Errorf("Couldn't restore: %v", err))
		return false, nil
	}
	s.lastChange = &change{kind: "restore", rows: restored}
	fmt.Fprintf(s.out, "%s%s Restored %s.\n", indent, s.st.green("✓"), entries(len(restored)))
	return true, nil
}

func (s *session) undo(ctx context.Context) error {
	c := s.lastChange
	if c == nil {
		fmt.Fprintln(s.out, indent+"Nothing to undo.")
		return nil
	}
	summary := func(rows []store.Transaction) string {
		var parts []string
		for i, tx := range rows {
			if i == 3 {
				parts = append(parts, "…")
				break
			}
			parts = append(parts, fmt.Sprintf("%s %s", tx.Kind, money.Format(tx.Amount)))
		}
		return strings.Join(parts, ", ")
	}
	var q string
	switch c.kind {
	case "edit":
		q = fmt.Sprintf("Undo the edit? %s goes back to %s [Y/n]: ", s.describe(c.after), s.describe(c.before))
	case "delete":
		q = fmt.Sprintf("Undo the delete of %s (%s)? [Y/n]: ", entries(len(c.rows)), summary(c.rows))
	case "restore":
		q = fmt.Sprintf("Undo the restore of %s (%s)? They go back to trash. [Y/n]: ", entries(len(c.rows)), summary(c.rows))
	}
	answer, err := s.readLine(q, false)
	if err != nil {
		return err
	}
	if a := strings.TrimSpace(answer); a != "" && !isYes(a) {
		fmt.Fprintln(s.out, indent+"Not undone.")
		return nil
	}

	ids := make([]int64, len(c.rows))
	for i, tx := range c.rows {
		ids[i] = tx.ID
	}
	switch c.kind {
	case "edit":
		_, err = s.store.UpdateTransaction(ctx, c.before.ID, c.before.Values(), c.after.UpdatedAt)
	case "delete":
		_, err = s.store.RestoreTransactions(ctx, ids)
	case "restore":
		_, err = s.store.DeleteTransactions(ctx, ids)
	}
	s.lastChange = nil
	switch {
	case errors.Is(err, store.ErrChanged), errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrCategoryKind):
		renderError(s.out, s.st, errors.New("That changed elsewhere since; undo skipped."))
	case err != nil:
		renderError(s.out, s.st, fmt.Errorf("Couldn't undo: %v", err))
	default:
		fmt.Fprintln(s.out, indent+s.st.green("✓")+" Undone.")
	}
	return nil
}

// Day-view input.

type dayAction int

const (
	actDone dayAction = iota
	actPrev
	actNext
	actDay
	actMonth
	actYear
	actPick
	actEditLine
)

type dayInput struct {
	action dayAction
	day    time.Time
	year   int
	month  time.Month
	nums   []int
	edit   AddCmd // actEditLine: the new values (At zero = keep date and time)
}

var (
	reDate  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reMonth = regexp.MustCompile(`^(\d{4})-(\d{1,2})$`)
	reYear  = regexp.MustCompile(`^\d{4}$`)
)

const dayInputHelp = "Type p, n, a date (2026-10-15), a month (2026-09), a year (2025), or entry number(s)."

// parseDayInput reads one answer in the day view.
func parseDayInput(line string, loc *time.Location) (dayInput, error) {
	f := strings.Fields(strings.ToLower(line))
	if len(f) == 0 || (len(f) == 1 && f[0] == "q") {
		return dayInput{action: actDone}, nil
	}
	if len(f) == 1 {
		w := f[0]
		switch {
		case w == "p":
			return dayInput{action: actPrev}, nil
		case w == "n":
			return dayInput{action: actNext}, nil
		case reDate.MatchString(w):
			d, err := parseDate(w, loc)
			if err != nil {
				return dayInput{}, err
			}
			return dayInput{action: actDay, day: d}, nil
		case reMonth.MatchString(w):
			m := reMonth.FindStringSubmatch(w)
			y, _ := strconv.Atoi(m[1])
			mon, _ := strconv.Atoi(m[2])
			if y < 1970 || mon < 1 || mon > 12 {
				return dayInput{}, fmt.Errorf("Invalid month %q. Use YYYY-MM, e.g. 2026-09", w)
			}
			return dayInput{action: actMonth, year: y, month: time.Month(mon)}, nil
		case reYear.MatchString(w):
			y, _ := strconv.Atoi(w)
			if y < 1970 {
				return dayInput{}, errors.New("Year must be 1970-9999.")
			}
			return dayInput{action: actYear, year: y, month: time.January}, nil
		}
	}
	if nums, err := parseNumbers(line); err == nil {
		return dayInput{action: actPick, nums: nums}, nil
	}
	// Edit shortcut: "<#> <type> <amount> [date time] ["note"]". Uses the original line so
	// the note keeps its case.
	first, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
	if n, err := strconv.Atoi(first); err == nil && n >= 1 && strings.TrimSpace(rest) != "" {
		word, tail, _ := strings.Cut(strings.TrimSpace(rest), " ")
		switch strings.ToLower(word) {
		case "pendapatan":
			word = "income"
		case "pengeluaran":
			word = "expenses"
		}
		cmd, err := Parse(word+" "+tail, loc)
		if err != nil {
			return dayInput{}, err
		}
		if add, ok := cmd.(AddCmd); ok {
			return dayInput{action: actEditLine, nums: []int{n}, edit: add}, nil
		}
	}
	return dayInput{}, errors.New(dayInputHelp)
}

// parseNumbers reads "2" or "1 3" (positive, duplicates removed, order kept).
func parseNumbers(line string) ([]int, error) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return nil, errors.New("no numbers")
	}
	var nums []int
	seen := map[int]bool{}
	for _, w := range f {
		n, err := strconv.Atoi(strings.TrimPrefix(w, "#"))
		if err != nil || n < 1 {
			return nil, errors.New("not a number")
		}
		if !seen[n] {
			seen[n] = true
			nums = append(nums, n)
		}
	}
	return nums, nil
}
