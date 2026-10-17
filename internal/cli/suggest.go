package cli

import (
	"strings"

	"money-manager/internal/lineedit"
	"money-manager/internal/store"
)

type commandInfo struct {
	name   string // what gets completed (a leading "/" is kept if the user typed one)
	label  string // how it is shown in the menu
	desc   string
	submit bool     // no required arguments: Enter on the suggestion runs it
	args   []string // hint shown after N arguments have been typed (see also argHint)
}

var commands = []commandInfo{
	{name: "income", label: "income", desc: "record money coming in"},
	{name: "expenses", label: "expenses", desc: "record money going out"},
	{name: "list", label: "list", desc: "show a month (Enter = current)", submit: true,
		args: []string{"[<month> <year>]", "<year>"}},
	{name: "edit", label: "edit", desc: "change a saved entry (pick it from a day)", submit: true,
		args: []string{"[YYYY-MM-DD]"}},
	{name: "delete", label: "delete", desc: "delete saved entries (pick them from a day)", submit: true,
		args: []string{"[YYYY-MM-DD]"}},
	{name: "trash", label: "trash", desc: "restore deleted entries (kept 30 days)", submit: true},
	{name: "undo", label: "undo", desc: "reverse the last edit, delete or restore", submit: true},
	{name: "categories", label: "categories", desc: "see the categories", submit: true},
	{name: "category", label: "category", desc: "add, rename or remove a category"},
	{name: "audio", label: "/audio", desc: "speak entries in Indonesian (voice input)", submit: true},
	{name: "tutorial", label: "/tutorial", desc: "guided walkthrough", submit: true},
	{name: "audio-status", label: "/audio-status", desc: "check if voice input can be used", submit: true},
	{name: "setup-audio", label: "/setup-audio", desc: "download the speech engine for voice input", submit: true},
	{name: "help", label: "/help", desc: "show all commands", submit: true},
	{name: "exit", label: "/exit", desc: "quit", submit: true},
}

var commandAliases = map[string]string{"expense": "expenses"}

var categoryActions = []struct{ name, desc string }{
	{"add", "add your own category"},
	{"rename", "rename one of your categories"},
	{"remove", "remove one of your categories"},
}

// Suggest returns completions for a partly typed command name, or an argument hint once
// the command name is complete. It doesn't know the user's categories; REPL.Suggest does.
func Suggest(line string) lineedit.Suggestions { return suggest(line, nil) }

func suggest(line string, cats []store.Category) lineedit.Suggestions {
	if strings.TrimSpace(line) == "" || line[0] == ' ' {
		return lineedit.Suggestions{}
	}

	if !strings.Contains(line, " ") {
		slash := ""
		if strings.HasPrefix(line, "/") {
			slash = "/"
		}
		word := strings.ToLower(strings.TrimPrefix(line, "/"))
		var items []lineedit.Item
		var exact *lineedit.Item
		for _, c := range commands {
			if !strings.HasPrefix(c.name, word) {
				continue
			}
			completed := slash + c.name
			if !c.submit {
				completed += " "
			}
			it := lineedit.Item{Line: completed, Label: c.label, Desc: c.desc, Submit: c.submit}
			if c.name == word {
				exact = &it
			} else {
				items = append(items, it)
			}
		}
		if len(items) > 0 {
			// A complete command that is also the start of others ("audio", "audio-status")
			// comes first, so Enter runs what was typed.
			if exact != nil {
				items = append([]lineedit.Item{*exact}, items...)
			}
			return lineedit.Suggestions{Items: items}
		}
		if hint := argHint(word, nil); hint != "" {
			return lineedit.Suggestions{Hint: " " + hint}
		}
		return lineedit.Suggestions{}
	}

	if strings.Contains(line, `"`) {
		return lineedit.Suggestions{} // typing a note
	}
	fields := strings.Fields(line)
	name := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if alias, ok := commandAliases[name]; ok {
		name = alias
	}
	if !strings.HasSuffix(line, " ") {
		// In the middle of typing an argument: complete a category or a category action.
		prefix := line[:strings.LastIndex(line, " ")+1]
		word := strings.ToLower(fields[len(fields)-1])
		switch {
		case (name == "income" || name == "expenses") && len(fields) == 3:
			kind := store.Expense
			if name == "income" {
				kind = store.Income
			}
			return lineedit.Suggestions{Items: categoryItems(prefix, word, kind, cats)}
		case name == "category" && len(fields) == 2:
			var items []lineedit.Item
			for _, a := range categoryActions {
				if strings.HasPrefix(a.name, word) && a.name != word {
					items = append(items, lineedit.Item{Line: prefix + a.name + " ", Label: a.name, Desc: a.desc})
				}
			}
			return lineedit.Suggestions{Items: items}
		}
		return lineedit.Suggestions{}
	}
	return lineedit.Suggestions{Hint: argHint(name, fields[1:])}
}

// categoryItems completes a category name of kind after the amount. A name typed in full
// gets no menu, so Enter saves the entry.
func categoryItems(prefix, word string, kind store.Kind, cats []store.Category) []lineedit.Item {
	var items []lineedit.Item
	for _, c := range cats {
		if c.Kind != kind || !strings.HasPrefix(c.Name, word) {
			continue
		}
		if c.Name == word {
			return nil
		}
		desc := "your category"
		if c.Builtin {
			desc = strings.Join(c.Aliases(), ", ")
		}
		items = append(items, lineedit.Item{Line: prefix + c.Name + " ", Label: c.Name, Desc: desc})
	}
	return items
}

// argHint returns the hint for what comes after the typed arguments of a command.
func argHint(name string, args []string) string {
	if alias, ok := commandAliases[name]; ok {
		name = alias
	}
	switch name {
	case "income", "expenses":
		return addHint(args)
	case "category":
		return categoryHint(args)
	}
	for _, c := range commands {
		if c.name == name && len(args) < len(c.args) {
			return c.args[len(args)]
		}
	}
	return ""
}

// addHint follows "<amount> [category] [YYYY-MM-DD HH:MM] ["note"]".
func addHint(args []string) string {
	if len(args) == 0 {
		return `<amount> [category] [YYYY-MM-DD HH:MM] ["note"]`
	}
	category, dated := false, false
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch {
		case !dated && reDateLike.MatchString(rest[i]):
			if i == len(rest)-1 {
				return "HH:MM"
			}
			dated = true
			i++ // the time
		case !category:
			category = true
		default:
			return "" // more words: a note without quotes
		}
	}
	switch {
	case !category && !dated:
		return `[category] [YYYY-MM-DD HH:MM] ["note"]`
	case !dated:
		return `[YYYY-MM-DD HH:MM] ["note"]`
	}
	return `["note"]`
}

func categoryHint(args []string) string {
	if len(args) == 0 {
		return "add | rename | remove"
	}
	n := len(args) - 1
	typed := n > 0
	if typed {
		if _, err := parseKind(args[1]); err != nil {
			typed = false
		}
	}
	switch strings.ToLower(args[0]) {
	case "add", "new":
		switch n {
		case 0:
			return "<income|expenses> <name>"
		case 1:
			return "<name>"
		}
	case "rename":
		hints := []string{"[income|expenses] <name> <new name>", "<new name>"}
		if typed {
			hints = []string{"", "<name> <new name>", "<new name>"}
		}
		if n < len(hints) && hints[n] != "" {
			return hints[n]
		}
	case "remove", "delete", "rm":
		if n == 0 {
			return "[income|expenses] <name>"
		}
		if n == 1 && typed {
			return "<name>"
		}
	}
	return ""
}
