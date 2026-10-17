package cli

import (
	"strings"

	"money-manager/internal/lineedit"
)

type commandInfo struct {
	name   string // what gets completed (a leading "/" is kept if the user typed one)
	label  string // how it is shown in the menu
	desc   string
	submit bool     // no required arguments: Enter on the suggestion runs it
	args   []string // hint shown after N arguments have been typed
}

var commands = []commandInfo{
	{name: "income", label: "income", desc: "record money coming in",
		args: []string{"<amount> [YYYY-MM-DD HH:MM]", "[YYYY-MM-DD HH:MM]", "HH:MM"}},
	{name: "expenses", label: "expenses", desc: "record money going out",
		args: []string{"<amount> [YYYY-MM-DD HH:MM]", "[YYYY-MM-DD HH:MM]", "HH:MM"}},
	{name: "list", label: "list", desc: "show a month (Enter = current)", submit: true,
		args: []string{"[<month> <year>]", "<year>"}},
	{name: "edit", label: "edit", desc: "change a saved entry (pick it from a day)", submit: true,
		args: []string{"[YYYY-MM-DD]"}},
	{name: "delete", label: "delete", desc: "delete saved entries (pick them from a day)", submit: true,
		args: []string{"[YYYY-MM-DD]"}},
	{name: "trash", label: "trash", desc: "restore deleted entries (kept 30 days)", submit: true},
	{name: "undo", label: "undo", desc: "reverse the last edit, delete or restore", submit: true},
	{name: "audio", label: "/audio", desc: "speak entries in Indonesian (voice input)", submit: true},
	{name: "tutorial", label: "/tutorial", desc: "guided walkthrough", submit: true},
	{name: "audio-status", label: "/audio-status", desc: "check if voice input can be used", submit: true},
	{name: "setup-audio", label: "/setup-audio", desc: "download the speech engine for voice input", submit: true},
	{name: "help", label: "/help", desc: "show all commands", submit: true},
	{name: "exit", label: "/exit", desc: "quit", submit: true},
}

var commandAliases = map[string]string{"expense": "expenses"}

// Suggest returns completions for a partly typed command name, or an argument hint once
// the command name is complete.
func Suggest(line string) lineedit.Suggestions {
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
		if hint := argHint(word, 0); hint != "" {
			return lineedit.Suggestions{Hint: " " + hint}
		}
		return lineedit.Suggestions{}
	}

	if !strings.HasSuffix(line, " ") {
		return lineedit.Suggestions{} // in the middle of typing an argument
	}
	fields := strings.Fields(line)
	name := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	return lineedit.Suggestions{Hint: argHint(name, len(fields)-1)}
}

func argHint(name string, typed int) string {
	if alias, ok := commandAliases[name]; ok {
		name = alias
	}
	for _, c := range commands {
		if c.name == name && typed < len(c.args) {
			return c.args[typed]
		}
	}
	return ""
}
