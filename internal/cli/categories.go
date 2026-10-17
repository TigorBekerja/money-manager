package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"money-manager/internal/money"
	"money-manager/internal/store"
)

// suggester holds what the suggestions need from the active session (the real one or
// the tutorial's): its categories.
type suggester struct {
	cats []store.Category
}

// categories returns the category list, loaded once and again after changes.
func (s *session) categories(ctx context.Context) ([]store.Category, error) {
	if s.cats == nil {
		cats, err := s.store.Categories(ctx)
		if err != nil {
			return nil, fmt.Errorf("Couldn't load categories: %v", err)
		}
		s.cats = cats
		if s.sug != nil {
			s.sug.cats = cats
		}
	}
	return s.cats, nil
}

// reloadCategories forgets the cached list, e.g. after a change or when a word isn't
// found (it may have been added from another window).
func (s *session) reloadCategories(ctx context.Context) ([]store.Category, error) {
	s.cats = nil
	return s.categories(ctx)
}

// findCategory looks up a typed name or alias of kind, reloading once if it's unknown.
func (s *session) findCategory(ctx context.Context, kind store.Kind, word string) (store.Category, []store.Category, bool, error) {
	cats, err := s.categories(ctx)
	if err != nil {
		return store.Category{}, nil, false, err
	}
	if c, ok := store.FindCategory(cats, kind, word); ok {
		return c, cats, true, nil
	}
	if cats, err = s.reloadCategories(ctx); err != nil {
		return store.Category{}, nil, false, err
	}
	c, ok := store.FindCategory(cats, kind, word)
	return c, cats, ok, nil
}

// kindCommand is the command that adds entries of kind.
func kindCommand(k store.Kind) string {
	if k == store.Income {
		return "income"
	}
	return "expenses"
}

func kindTitle(k store.Kind) string {
	if k == store.Income {
		return "Income"
	}
	return "Expenses"
}

// addExample renders an add command in its usual form, for hints.
func (s *session) addExample(c AddCmd, category string) string {
	e := kindCommand(c.Kind) + " " + plainAmount(c.Amount)
	if category != "" {
		e += " " + category
	}
	if !c.At.IsZero() {
		e += " " + c.At.In(s.loc()).Format(timeLayout)
	}
	return e
}

// categoryOf resolves the category typed in an add command or an edit shortcut. The zero
// Category means none. Errors are meant for the user.
func (s *session) categoryOf(ctx context.Context, c AddCmd) (store.Category, error) {
	if c.Category == "" {
		return store.Category{}, nil
	}
	none := strings.EqualFold(c.Category, "none")
	cat, cats, ok := store.Category{}, []store.Category(nil), false
	if !none {
		var err error
		if cat, cats, ok, err = s.findCategory(ctx, c.Kind, c.Category); err != nil {
			return store.Category{}, err
		}
	}
	if len(c.Unquoted) > 0 {
		if ok || none {
			name := cat.Name
			if none {
				name = "none"
			}
			return store.Category{}, fmt.Errorf(`Put the note in quotes, e.g. %s "%s"`, s.addExample(c, name), strings.Join(c.Unquoted, " "))
		}
		words := append([]string{c.Category}, c.Unquoted...)
		return store.Category{}, fmt.Errorf(`Put the note in quotes, e.g. %s "%s"`, s.addExample(c, ""), strings.Join(words, " "))
	}
	if none || ok {
		return cat, nil
	}
	return store.Category{}, unknownCategory(cats, c.Kind, c.Category,
		fmt.Sprintf(`Type categories to see them all, or put a note in quotes: %s "%s"`, s.addExample(c, ""), c.Category))
}

// unknownCategory explains a word that isn't a category of kind; tail ends the message.
func unknownCategory(cats []store.Category, kind store.Kind, word, tail string) error {
	other := store.Income
	if kind == store.Income {
		other = store.Expense
	}
	if c, ok := store.FindCategory(cats, other, word); ok {
		title := "Expense"
		if kind == store.Income {
			title = "Income"
		}
		return fmt.Errorf("%s is %s category. %s categories: %s", c.Name, articleOf(other), title, categoryNames(cats, kind))
	}
	word = strings.ToLower(word)
	if c, ok := store.SimilarCategory(cats, kind, word); ok {
		return fmt.Errorf("Unknown %s category %q. Did you mean %s? %s", kind, word, c.Name, tail)
	}
	return fmt.Errorf("Unknown %s category %q. %s", kind, word, tail)
}

func articleOf(k store.Kind) string {
	if k == store.Income {
		return "an income"
	}
	return "an expense"
}

func categoryNames(cats []store.Category, kind store.Kind) string {
	var names []string
	for _, c := range cats {
		if c.Kind == kind {
			names = append(names, c.Name)
		}
	}
	return strings.Join(names, ", ")
}

// askCategory asks for the category of an entry of kind: Enter keeps current, "-" or
// "none" removes it.
func (s *session) askCategory(ctx context.Context, kind store.Kind, current store.Category) (store.Category, error) {
	prompt := "Category (Enter: none): "
	if current.ID != 0 {
		prompt = fmt.Sprintf("Category [%s] (- to remove): ", current.Name)
	}
	return askField(s, prompt, current, func(line string) (store.Category, error) {
		if line == "-" || strings.EqualFold(line, "none") {
			return store.Category{}, nil
		}
		c, cats, ok, err := s.findCategory(ctx, kind, line)
		if err != nil || ok {
			return c, err
		}
		return store.Category{}, unknownCategory(cats, kind, line,
			fmt.Sprintf("Choose one of: %s (or - for none).", categoryNames(cats, kind)))
	})
}

// The categories screen and category add / rename / remove.

func (s *session) showCategories(ctx context.Context) error {
	cats, err := s.reloadCategories(ctx)
	if err == nil {
		var counts map[int64]int
		if counts, err = s.store.CategoryCounts(ctx); err == nil {
			s.renderCategories(cats, counts)
			return nil
		}
	}
	renderError(s.out, s.st, err)
	return nil
}

func (s *session) renderCategories(cats []store.Category, counts map[int64]int) {
	nameW := len("subscription")
	for _, c := range cats {
		nameW = max(nameW, len(c.Name))
	}
	const countW = len("entries")
	fmt.Fprintln(s.out)
	for _, kind := range []store.Kind{store.Expense, store.Income} {
		fmt.Fprintln(s.out, indent+s.st.bold(padRight(kindTitle(kind), 2+nameW+2))+s.st.dim(padLeft("entries", countW)))
		for _, c := range cats {
			if c.Kind != kind {
				continue
			}
			words := s.st.dim("added by you")
			if c.Builtin {
				words = s.st.dim(strings.Join(c.Aliases(), ", "))
			}
			fmt.Fprintln(s.out, indent+"  "+s.st.kind(kind, padRight(c.Name, nameW))+"  "+
				padLeft(strconv.Itoa(counts[c.ID]), countW)+"   "+words)
		}
	}
	fmt.Fprintln(s.out)
	fmt.Fprintln(s.out, indent+"Use one after the amount: expenses 25000 food \"makan siang\"")
	fmt.Fprintln(s.out, indent+s.st.dim("category add <income|expenses> <name> · category rename <name> <new name> · category remove <name>"))
}

func (s *session) categoryCmd(ctx context.Context, c CategoryCmd) error {
	cats, err := s.reloadCategories(ctx)
	if err != nil {
		renderError(s.out, s.st, err)
		return nil
	}
	practice := ""
	if s.practice {
		practice = "   " + s.st.dim("(practice, not saved)")
	}
	if c.Action == "add" {
		if err := store.CheckCategoryName(cats, c.Kind, c.Name, 0); err != nil {
			renderError(s.out, s.st, err)
			return nil
		}
		added, err := s.store.AddCategory(ctx, c.Kind, c.Name)
		s.reloadCategories(ctx)
		if err != nil {
			renderError(s.out, s.st, categoryError(err, "add"))
			return nil
		}
		fmt.Fprintf(s.out, "%s%s Added %s category %s.%s\n", indent, s.st.green("✓"), added.Kind, added.Name, practice)
		return nil
	}

	cat, err := pickCategory(cats, c)
	if err != nil {
		renderError(s.out, s.st, err)
		return nil
	}
	verb := map[string]string{"rename": "renamed", "remove": "removed"}[c.Action]
	if cat.Builtin {
		renderError(s.out, s.st, fmt.Errorf("%s is built in and can't be %s.", cat.Name, verb))
		return nil
	}
	counts, err := s.store.CategoryCounts(ctx)
	if err != nil {
		renderError(s.out, s.st, err)
		return nil
	}
	n := counts[cat.ID]

	if c.Action == "rename" {
		if err := store.CheckCategoryName(cats, cat.Kind, c.NewName, cat.ID); err != nil {
			renderError(s.out, s.st, err)
			return nil
		}
		renamed, err := s.store.RenameCategory(ctx, cat.ID, c.NewName)
		s.reloadCategories(ctx)
		if err != nil {
			renderError(s.out, s.st, categoryError(err, "rename"))
			return nil
		}
		fmt.Fprintf(s.out, "%s%s Renamed %s to %s (%s).%s\n", indent, s.st.green("✓"), cat.Name, renamed.Name, entries(n), practice)
		return nil
	}

	var target store.Category
	q := fmt.Sprintf("Remove %s? [y/N]: ", cat.Name)
	if n > 0 {
		prompt := fmt.Sprintf("%s use %s. Move them to (category, or Enter for none): ", entriesCap(n), cat.Name)
		target, err = askField(s, prompt, store.Category{}, func(line string) (store.Category, error) {
			if line == "-" || strings.EqualFold(line, "none") {
				return store.Category{}, nil
			}
			t, ok := store.FindCategory(cats, cat.Kind, line)
			switch {
			case ok && t.ID == cat.ID:
				return store.Category{}, fmt.Errorf("Choose another category than %s.", cat.Name)
			case ok:
				return t, nil
			}
			return store.Category{}, unknownCategory(cats, cat.Kind, line,
				fmt.Sprintf("Choose one of: %s (or Enter for none).", categoryNames(cats, cat.Kind)))
		})
		if err != nil {
			return err
		}
		q = fmt.Sprintf("Remove %s? Its %s will have no category. [y/N]: ", cat.Name, entries(n))
		if target.ID != 0 {
			q = fmt.Sprintf("Remove %s and move %s to %s? [y/N]: ", cat.Name, entries(n), target.Name)
		}
	}
	answer, err := s.readLine(q, false)
	if err != nil {
		return err
	}
	if !isYes(answer) {
		fmt.Fprintln(s.out, indent+"Nothing removed.")
		return nil
	}
	moved, err := s.store.RemoveCategory(ctx, cat.ID, target.ID)
	s.reloadCategories(ctx)
	if err != nil {
		renderError(s.out, s.st, categoryError(err, "remove"))
		return nil
	}
	msg := fmt.Sprintf("Removed %s.", cat.Name)
	switch {
	case moved > 0 && target.ID != 0:
		msg = fmt.Sprintf("Removed %s; %s moved to %s.", cat.Name, entries(moved), target.Name)
	case moved == 1:
		msg = fmt.Sprintf("Removed %s; 1 entry now has no category.", cat.Name)
	case moved > 1:
		msg = fmt.Sprintf("Removed %s; %d entries now have no category.", cat.Name, moved)
	}
	fmt.Fprintf(s.out, "%s%s %s%s\n", indent, s.st.green("✓"), msg, practice)
	return nil
}

// pickCategory finds the category a rename/remove refers to, by name or alias. Without a
// type it looks in both and asks for one if both have the name.
func pickCategory(cats []store.Category, c CategoryCmd) (store.Category, error) {
	kinds := []store.Kind{store.Expense, store.Income}
	if c.Kind != "" {
		kinds = []store.Kind{c.Kind}
	}
	var found []store.Category
	for _, k := range kinds {
		if cat, ok := store.FindCategory(cats, k, c.Name); ok {
			found = append(found, cat)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 2:
		example := fmt.Sprintf("category %s expenses %s", c.Action, c.Name)
		if c.Action == "rename" {
			example += " " + c.NewName
		}
		return store.Category{}, fmt.Errorf("Both types have a category %s. Say which: %s", c.Name, example)
	}
	for _, k := range kinds {
		if cat, ok := store.SimilarCategory(cats, k, c.Name); ok {
			return store.Category{}, fmt.Errorf("No category %q. Did you mean %s?", c.Name, cat.Name)
		}
	}
	return store.Category{}, fmt.Errorf("No category %q. Type categories to see them all.", c.Name)
}

func categoryError(err error, action string) error {
	switch {
	case errors.Is(err, store.ErrCategoryExists):
		return errors.New("That name was just taken (another window?). Type categories to see them all.")
	case errors.Is(err, store.ErrNotFound):
		return errors.New("That category was removed elsewhere.")
	case errors.Is(err, store.ErrBuiltin):
		return errors.New("Built-in categories can't be changed.")
	case errors.Is(err, store.ErrCategoryKind):
		return errors.New("That category doesn't fit (it was changed elsewhere). Nothing was removed.")
	}
	return fmt.Errorf("Couldn't %s the category: %v", action, err)
}

func entriesCap(n int) string {
	e := entries(n)
	return strings.ToUpper(e[:1]) + e[1:]
}

// Category column and breakdown.

// categoryColumn is an optional column in the middle of a table: left out when no row
// has a category.
type categoryColumn struct {
	sep string
	w   int // 0 = no column
}

func newCategoryColumn(txs []store.Transaction, sep string) categoryColumn {
	names := make([]string, len(txs))
	for i, tx := range txs {
		names[i] = tx.Category
	}
	return newCategoryColumnOf(names, sep)
}

func newCategoryColumnOf(names []string, sep string) categoryColumn {
	c := categoryColumn{sep: sep}
	for _, n := range names {
		if n != "" {
			c.w = max(c.w, len("Category"), utf8.RuneCountInString(n))
		}
	}
	return c
}

func (c categoryColumn) width() int {
	if c.w == 0 {
		return 0
	}
	return c.w + len(c.sep)
}

func (c categoryColumn) header() string {
	if c.w == 0 {
		return ""
	}
	return padRight("Category", c.w) + c.sep
}

func (c categoryColumn) cell(name string) string {
	if c.w == 0 {
		return ""
	}
	return padRight(name, c.w) + c.sep
}

// renderBreakdown shows, per type, how much went to each category, largest first and
// "(none)" last. A type is left out when none of its entries has a category.
func renderBreakdown(w io.Writer, st styles, txs []store.Transaction) {
	type section struct {
		kind  store.Kind
		sums  map[string]int64 // by category name; "" = none
		total int64
	}
	var sections []section
	nameW, amtW := len("(none)"), 0
	for _, kind := range []store.Kind{store.Expense, store.Income} {
		sec := section{kind: kind, sums: map[string]int64{}}
		categorized := false
		for _, tx := range txs {
			if tx.Kind == kind {
				sec.sums[tx.Category] += tx.Amount
				sec.total += tx.Amount
				categorized = categorized || tx.Category != ""
			}
		}
		if !categorized {
			continue
		}
		for n, v := range sec.sums {
			nameW = max(nameW, utf8.RuneCountInString(n))
			amtW = max(amtW, len(money.Format(v)))
		}
		sections = append(sections, sec)
	}
	for _, sec := range sections {
		kind, sums, total := sec.kind, sec.sums, sec.total
		names := make([]string, 0, len(sums))
		for n := range sums {
			names = append(names, n)
		}
		slices.SortFunc(names, func(a, b string) int {
			switch {
			case a == "":
				return 1
			case b == "":
				return -1
			case sums[a] != sums[b]:
				if sums[a] > sums[b] {
					return -1
				}
				return 1
			}
			return strings.Compare(a, b)
		})
		fmt.Fprintln(w)
		fmt.Fprintln(w, indent+" "+st.bold(kindTitle(kind)+" by category"))
		for _, n := range names {
			label := n
			if n == "" {
				label = "(none)"
			}
			fmt.Fprintln(w, indent+"   "+padRight(label, nameW)+"   "+
				st.kind(kind, padLeft(money.Format(sums[n]), amtW))+"   "+padLeft(strconv.Itoa(percent(sums[n], total))+"%", 4))
		}
	}
}

// percent returns part/total in whole percent, rounded half up. Integer math (amounts
// can be large enough that part*100 overflows int64).
func percent(part, total int64) int {
	if total <= 0 {
		return 0
	}
	p := new(big.Int).Mul(big.NewInt(part), big.NewInt(200))
	p.Add(p, big.NewInt(total))
	p.Quo(p, new(big.Int).Mul(big.NewInt(total), big.NewInt(2)))
	return int(p.Int64())
}
