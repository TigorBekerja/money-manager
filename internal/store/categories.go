package store

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Category groups entries of one kind ("food" for expenses, "salary" for income).
type Category struct {
	ID      int64
	Kind    Kind
	Name    string
	Builtin bool // shipped with the app: can't be renamed or removed
}

// MaxCategoryLen is the longest category name, in characters.
const MaxCategoryLen = 20

var (
	// ErrCategoryExists means the kind already has a category (or built-in alias) by that name.
	ErrCategoryExists = errors.New("category already exists")
	// ErrBuiltin means a built-in category can't be renamed or removed.
	ErrBuiltin = errors.New("built-in category")
	// ErrCategoryKind means an entry's category doesn't exist or is for the other kind.
	ErrCategoryKind = errors.New("category doesn't exist or is for the other type")
)

// builtinCategories are seeded by migration 004 in this order. The aliases are the
// Indonesian words that also mean the category, typed or spoken.
var builtinCategories = []struct {
	kind    Kind
	name    string
	aliases []string
}{
	{Expense, "food", []string{"makanan"}},
	{Expense, "drink", []string{"minuman"}},
	{Expense, "dessert", []string{"pencuci mulut", "hidangan penutup", "makanan penutup"}},
	{Expense, "laundry", []string{"londri", "cucian", "binatu"}},
	{Expense, "subscription", []string{"langganan"}},
	{Expense, "transport", []string{"transportasi", "transpor", "transpot"}},
	{Income, "salary", []string{"gaji"}},
}

// Aliases returns the other words for a built-in category ("makanan" for food).
func (c Category) Aliases() []string {
	if !c.Builtin {
		return nil
	}
	for _, b := range builtinCategories {
		if b.kind == c.Kind && b.name == c.Name {
			return b.aliases
		}
	}
	return nil
}

// Words returns every way to say the category, each as a list of words: the name
// ("uang-sekolah" is "uang sekolah") and the aliases.
func (c Category) Words() [][]string {
	out := [][]string{strings.Split(c.Name, "-")}
	for _, a := range c.Aliases() {
		out = append(out, strings.Fields(a))
	}
	return out
}

var reCategoryName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// CheckCategoryName reports why name can't be a new name for a category of kind, given
// the existing categories (the one being renamed is skipped by its id). The messages
// are meant for the user.
func CheckCategoryName(cats []Category, kind Kind, name string, renaming int64) error {
	switch {
	case name == "none":
		return errors.New(`"none" means no category; choose another name.`)
	case slices.Contains([]string{"income", "expense", "expenses", "pendapatan", "pengeluaran"}, name):
		return fmt.Errorf("%s is a type, not a category; choose another name.", name)
	case len(name) > MaxCategoryLen:
		return fmt.Errorf("A category name can have at most %d characters.", MaxCategoryLen)
	case !reCategoryName.MatchString(name):
		return errors.New("A category name is one word: letters, digits and -, starting with a letter (e.g. parkir).")
	}
	for _, c := range cats {
		if c.Kind != kind || c.ID == renaming {
			continue
		}
		if c.Name == name {
			return nameTaken(fmt.Sprintf("There is already %s category %s.", article(kind), name))
		}
		for _, a := range c.Aliases() {
			if strings.ReplaceAll(name, "-", " ") == a {
				return nameTaken(fmt.Sprintf("%s already means %s.", name, c.Name))
			}
		}
	}
	return nil
}

// nameTaken is a user-facing message that matches ErrCategoryExists.
type nameTaken string

func (e nameTaken) Error() string        { return string(e) }
func (e nameTaken) Is(target error) bool { return target == ErrCategoryExists }

func article(k Kind) string {
	if k == Income {
		return "an income"
	}
	return "an expense"
}

// FindCategory looks up a typed word (any case) among the categories of kind: a name or
// a one-word alias.
func FindCategory(cats []Category, kind Kind, word string) (Category, bool) {
	word = strings.ToLower(word)
	for _, c := range cats {
		if c.Kind == kind && (c.Name == word || slices.Contains(c.Aliases(), word)) {
			return c, true
		}
	}
	return Category{}, false
}

// SimilarCategory finds the category of kind closest to a mistyped word ("fod" → food),
// within 1 edit for short words and 2 for words of 5+ letters.
func SimilarCategory(cats []Category, kind Kind, word string) (Category, bool) {
	word = strings.ToLower(word)
	limit := 1
	if len([]rune(word)) >= 5 {
		limit = 2
	}
	var best Category
	bestD := limit + 1
	for _, c := range cats {
		if c.Kind != kind {
			continue
		}
		for _, w := range append([]string{c.Name}, c.Aliases()...) {
			if d := editDistance(word, w); d < bestD {
				best, bestD = c, d
			}
		}
	}
	return best, bestD <= limit
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// SortCategories orders categories for display: expenses first, then income; built-ins in
// their fixed order, then the user's own by name.
func SortCategories(cats []Category) {
	rank := func(c Category) int {
		for i, b := range builtinCategories {
			if c.Builtin && b.kind == c.Kind && b.name == c.Name {
				return i
			}
		}
		return len(builtinCategories)
	}
	slices.SortFunc(cats, func(a, b Category) int {
		if a.Kind != b.Kind {
			if a.Kind == Expense {
				return -1
			}
			return 1
		}
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		return strings.Compare(a.Name, b.Name)
	})
}
