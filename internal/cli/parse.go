package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"money-manager/internal/money"
	"money-manager/internal/store"
)

type Command interface{ isCommand() }

// AddCmd records a transaction. A zero At means "now". HasNote tells a quoted note
// (possibly "") from no note at all, which matters for the edit shortcut (keep vs set).
// Category is the word typed for the category, as typed ("" = none given, "none" = no
// category); the executor resolves it, since only the store knows the user's categories.
// Unquoted holds any further words: a note without quotes, which the executor reports.
type AddCmd struct {
	Kind     store.Kind
	Amount   int64
	At       time.Time
	Note     string
	HasNote  bool
	Category string
	Unquoted []string
}

// CategoriesCmd lists the categories.
type CategoriesCmd struct{}

// CategoryCmd adds, renames or removes a category. Kind is empty when not given (rename
// and remove then look in both types).
type CategoryCmd struct {
	Action  string // "add", "rename" or "remove"
	Kind    store.Kind
	Name    string
	NewName string
}

// ListCmd shows one month. Prompt means month and year were not given and must be asked.
type ListCmd struct {
	Month  time.Month
	Year   int
	Prompt bool
}

// EditCmd and DeleteCmd open the day view; a zero Day means today.
type EditCmd struct{ Day time.Time }
type DeleteCmd struct{ Day time.Time }
type TrashCmd struct{}
type UndoCmd struct{}

type TutorialCmd struct{}
type AudioCmd struct{}
type AudioStatusCmd struct{}
type SetupAudioCmd struct{}
type HelpCmd struct{}
type ExitCmd struct{}

func (AddCmd) isCommand()         {}
func (CategoriesCmd) isCommand()  {}
func (CategoryCmd) isCommand()    {}
func (ListCmd) isCommand()        {}
func (EditCmd) isCommand()        {}
func (DeleteCmd) isCommand()      {}
func (TrashCmd) isCommand()       {}
func (UndoCmd) isCommand()        {}
func (TutorialCmd) isCommand()    {}
func (AudioCmd) isCommand()       {}
func (AudioStatusCmd) isCommand() {}
func (SetupAudioCmd) isCommand()  {}
func (HelpCmd) isCommand()        {}
func (ExitCmd) isCommand()        {}

const (
	dateTimeHint = "Use YYYY-MM-DD HH:MM, e.g. 2026-10-17 09:00"
	listUsage    = "Usage: list <month> <year>"
)

// Parse turns one input line into a Command. An empty line returns (nil, nil).
// Dates and times are interpreted in loc.
func Parse(line string, loc *time.Location) (Command, error) {
	rest, note, hasNote, noteErr := splitNote(line)
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		if noteErr != nil || hasNote {
			return nil, errors.New(`Start with a command, e.g. expenses 25000 "makan siang"`)
		}
		return nil, nil
	}
	name := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	args := fields[1:]

	switch name {
	case "income", "expenses", "expense":
		if noteErr != nil {
			return nil, noteErr
		}
		kind := store.Expense
		if name == "income" {
			kind = store.Income
		}
		cmd, err := parseAdd(kind, name, args, loc)
		if err != nil {
			return nil, err
		}
		cmd.Note, cmd.HasNote = note, hasNote
		return cmd, nil
	}
	if noteErr != nil || hasNote {
		return nil, errors.New("Only income and expenses take a note in quotes.")
	}

	switch name {
	case "list":
		return parseList(args)
	case "edit", "delete":
		return parseDayCommand(name, args, loc)
	case "categories":
		return CategoriesCmd{}, nil
	case "category":
		return parseCategory(args)
	case "trash":
		return TrashCmd{}, nil
	case "undo":
		return UndoCmd{}, nil
	case "tutorial":
		return TutorialCmd{}, nil
	case "audio":
		return AudioCmd{}, nil
	case "audio-status":
		return AudioStatusCmd{}, nil
	case "setup-audio":
		return SetupAudioCmd{}, nil
	case "help", "?":
		return HelpCmd{}, nil
	case "exit", "quit", "q":
		return ExitCmd{}, nil
	}
	return nil, fmt.Errorf("Unknown command %q. Type /help to see commands.", fields[0])
}

var (
	reDateLike = regexp.MustCompile(`^\d{1,4}[-/.]\d{1,2}[-/.]\d{1,4}$`)
	reTimeLike = regexp.MustCompile(`^\d{1,2}[:.]\d{1,2}$|^\d{1,2}(am|pm)$`)
)

func parseAdd(kind store.Kind, name string, args []string, loc *time.Location) (AddCmd, error) {
	if len(args) == 0 {
		return AddCmd{}, fmt.Errorf(`Missing amount. Usage: %s <amount> [category] [YYYY-MM-DD HH:MM] ["note"]   e.g. %s 50000`, name, name)
	}
	amount, err := money.Parse(args[0])
	if err != nil {
		return AddCmd{}, err
	}
	cmd := AddCmd{Kind: kind, Amount: amount}

	// A date must be followed by a time. Other words: the first is the category (before
	// or after the date), the rest an unquoted note.
	var words []string
	for rest := args[1:]; len(rest) > 0; {
		w := rest[0]
		switch {
		case cmd.At.IsZero() && reDateLike.MatchString(w):
			if len(rest) == 1 || !reTimeLike.MatchString(strings.ToLower(rest[1])) {
				return AddCmd{}, errors.New("Missing time. " + dateTimeHint)
			}
			if cmd.At, err = parseDateTime(w, rest[1], loc); err != nil {
				return AddCmd{}, err
			}
			rest = rest[2:]
			continue
		case cmd.At.IsZero() && reTimeLike.MatchString(strings.ToLower(w)):
			return AddCmd{}, errors.New("Missing date. " + dateTimeHint)
		}
		words = append(words, w)
		rest = rest[1:]
	}
	if len(words) > 0 {
		cmd.Category = words[0]
	}
	if len(words) > 1 {
		cmd.Unquoted = words[1:]
	}
	return cmd, nil
}

const categoryUsage = "Usage: category add <income|expenses> <name> · category rename <name> <new name> · category remove <name>"

// parseCategory reads "add <type> <name>", "rename [type] <name> <new>" and
// "remove [type] <name>". Names are lowercased; the executor checks them.
func parseCategory(args []string) (Command, error) {
	if len(args) == 0 {
		return nil, errors.New(categoryUsage)
	}
	action := strings.ToLower(args[0])
	rest := args[1:]
	for i := range rest {
		rest[i] = strings.ToLower(rest[i])
	}
	var kind store.Kind
	if len(rest) > 0 {
		if k, err := parseKind(rest[0]); err == nil {
			kind, rest = k, rest[1:]
		}
	}
	switch action {
	case "add", "new":
		switch {
		case kind == "" && len(rest) == 1:
			return nil, fmt.Errorf("Say the type first: category add expenses %s (or income)", rest[0])
		case kind == "" || len(rest) != 1:
			return nil, errors.New("Usage: category add <income|expenses> <name>   e.g. category add expenses parkir")
		}
		return CategoryCmd{Action: "add", Kind: kind, Name: rest[0]}, nil
	case "rename":
		if len(rest) != 2 {
			return nil, errors.New("Usage: category rename [income|expenses] <name> <new name>   e.g. category rename parkir parking")
		}
		return CategoryCmd{Action: "rename", Kind: kind, Name: rest[0], NewName: rest[1]}, nil
	case "remove", "delete", "rm":
		if len(rest) != 1 {
			return nil, errors.New("Usage: category remove [income|expenses] <name>   e.g. category remove parkir")
		}
		return CategoryCmd{Action: "remove", Kind: kind, Name: rest[0]}, nil
	}
	return nil, errors.New(categoryUsage)
}

// splitNote separates a trailing "quoted note" from the rest of the line. The note must
// be last; runs of spaces in it collapse to one.
func splitNote(line string) (rest, note string, hasNote bool, err error) {
	open := strings.IndexByte(line, '"')
	if open < 0 {
		return line, "", false, nil
	}
	end := strings.IndexByte(line[open+1:], '"')
	if end < 0 {
		return line[:open], "", false, errors.New(`The note is missing its closing quote "`)
	}
	if after := strings.TrimSpace(line[open+1+end+1:]); after != "" {
		return line[:open], "", false, errors.New(`The note must be the last part, e.g. expenses 25000 "makan siang"`)
	}
	note, err = cleanNote(line[open+1 : open+1+end])
	return line[:open], note, true, err
}

// cleanNote collapses spaces and checks the length of a note.
func cleanNote(s string) (string, error) {
	note := strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(note) > store.MaxNoteLen {
		return "", fmt.Errorf("Note is too long (max %d characters).", store.MaxNoteLen)
	}
	return note, nil
}

func parseDateTime(date, clock string, loc *time.Location) (time.Time, error) {
	d, err := parseDate(date, loc)
	if err != nil {
		return time.Time{}, err
	}
	c, err := parseClock(clock)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(d.Year(), d.Month(), d.Day(), c.Hour(), c.Minute(), 0, 0, loc), nil
}

func parseDate(date string, loc *time.Location) (time.Time, error) {
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil || d.Year() < 1970 {
		return time.Time{}, fmt.Errorf("Invalid date %q. %s", date, dateTimeHint)
	}
	return d, nil
}

func parseClock(clock string) (time.Time, error) {
	c, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, fmt.Errorf("Invalid time %q. Use 24-hour HH:MM, e.g. 21:00", clock)
	}
	return c, nil
}

// ParseEditTime reads a changed date/time for a recorded entry: "YYYY-MM-DD HH:MM",
// "YYYY-MM-DD" (keeps base's time) or "HH:MM" (keeps base's date).
func ParseEditTime(s string, base time.Time) (time.Time, error) {
	loc := base.Location()
	fields := strings.Fields(s)
	switch {
	case len(fields) == 2:
		return parseDateTime(fields[0], fields[1], loc)
	case len(fields) == 1 && strings.Contains(fields[0], "-"):
		d, err := parseDate(fields[0], loc)
		if err != nil {
			return time.Time{}, err
		}
		return time.Date(d.Year(), d.Month(), d.Day(), base.Hour(), base.Minute(), 0, 0, loc), nil
	case len(fields) == 1 && strings.Contains(fields[0], ":"):
		c, err := parseClock(fields[0])
		if err != nil {
			return time.Time{}, err
		}
		return time.Date(base.Year(), base.Month(), base.Day(), c.Hour(), c.Minute(), 0, 0, loc), nil
	}
	return time.Time{}, errors.New("Use YYYY-MM-DD HH:MM, YYYY-MM-DD or HH:MM, e.g. 2026-10-15 09:00")
}

func parseDayCommand(name string, args []string, loc *time.Location) (Command, error) {
	usage := fmt.Sprintf("Usage: %s [YYYY-MM-DD], then choose the entry by its number", name)
	var day time.Time
	switch len(args) {
	case 0:
	case 1:
		d, err := parseDate(args[0], loc)
		if err != nil {
			return nil, fmt.Errorf("%v. %s", strings.TrimSuffix(err.Error(), "."), usage)
		}
		day = d
	default:
		return nil, errors.New(usage)
	}
	if name == "edit" {
		return EditCmd{Day: day}, nil
	}
	return DeleteCmd{Day: day}, nil
}

func parseList(args []string) (Command, error) {
	switch len(args) {
	case 0:
		return ListCmd{Prompt: true}, nil
	case 2:
	default:
		return nil, errors.New(listUsage + "   e.g. list 10 2026")
	}
	m, err := ParseMonth(args[0])
	if err != nil {
		return nil, fmt.Errorf("%v %s", err, listUsage)
	}
	y, err := ParseYear(args[1])
	if err != nil {
		return nil, fmt.Errorf("%v %s", err, listUsage)
	}
	return ListCmd{Month: m, Year: y}, nil
}

func ParseMonth(s string) (time.Month, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 12 {
		return 0, errors.New("Month must be 1-12.")
	}
	return time.Month(n), nil
}

func ParseYear(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || len(s) != 4 || n < 1970 {
		return 0, errors.New("Year must be 1970-9999.")
	}
	return n, nil
}
