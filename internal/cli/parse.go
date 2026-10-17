package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"money-manager/internal/money"
	"money-manager/internal/store"
)

type Command interface{ isCommand() }

// AddCmd records a transaction. A zero At means "now".
type AddCmd struct {
	Kind   store.Kind
	Amount int64
	At     time.Time
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
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil, nil
	}
	name := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	args := fields[1:]

	switch name {
	case "income":
		return parseAdd(store.Income, "income", args, loc)
	case "expenses", "expense":
		return parseAdd(store.Expense, name, args, loc)
	case "list":
		return parseList(args)
	case "edit", "delete":
		return parseDayCommand(name, args, loc)
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

func parseAdd(kind store.Kind, name string, args []string, loc *time.Location) (Command, error) {
	usage := fmt.Sprintf("Usage: %s <amount> [YYYY-MM-DD HH:MM]", name)
	switch len(args) {
	case 0:
		return nil, fmt.Errorf("Missing amount. %s   e.g. %s 50000", usage, name)
	case 1, 3:
	case 2:
		return nil, errors.New("Missing time. " + dateTimeHint)
	default:
		return nil, errors.New("Too many arguments. " + usage)
	}

	amount, err := money.Parse(args[0])
	if err != nil {
		return nil, err
	}
	cmd := AddCmd{Kind: kind, Amount: amount}
	if len(args) == 3 {
		if cmd.At, err = parseDateTime(args[1], args[2], loc); err != nil {
			return nil, err
		}
	}
	return cmd, nil
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
