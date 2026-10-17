package cli

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"money-manager/internal/money"
	"money-manager/internal/store"
)

const (
	indent     = "  "
	timeLayout = "2006-01-02 15:04"
)

type styles struct{ on bool }

func (s styles) wrap(code, text string) string {
	if !s.on || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s styles) bold(t string) string   { return s.wrap("1", t) }
func (s styles) dim(t string) string    { return s.wrap("2", t) }
func (s styles) green(t string) string  { return s.wrap("32", t) }
func (s styles) red(t string) string    { return s.wrap("31", t) }
func (s styles) yellow(t string) string { return s.wrap("33", t) }

func (s styles) kind(k store.Kind, text string) string {
	if k == store.Income {
		return s.green(text)
	}
	return s.red(text)
}

func (s styles) signed(cents int64, text string) string {
	if cents < 0 {
		return s.red(text)
	}
	return s.green(text)
}

func padRight(s string, n int) string {
	return s + strings.Repeat(" ", max(0, n-utf8.RuneCountInString(s)))
}
func padLeft(s string, n int) string {
	return strings.Repeat(" ", max(0, n-utf8.RuneCountInString(s))) + s
}

// BannerInfo is shown in the startup box.
type BannerInfo struct {
	Version string
	Name    string // greeting name, e.g. "Vandika"
	DB      string // e.g. "localhost:5432/money_manager"
}

func renderBanner(w io.Writer, st styles, b BannerInfo) {
	greeting := "Hi! Type /help to see commands."
	if b.Name != "" {
		greeting = "Hi " + b.Name + "! Type /help to see commands."
	}
	lines := []string{
		"",
		greeting,
		"New here? Type /tutorial for a quick guide.",
	}
	if b.DB != "" {
		lines = append(lines, "db: "+b.DB)
	}

	title, version := "Money Manager", "v"+b.Version
	width := utf8.RuneCountInString(title) + 2 + len(version)
	for _, l := range lines {
		width = max(width, utf8.RuneCountInString(l))
	}

	bar := strings.Repeat("─", width+4)
	fmt.Fprintln(w)
	fmt.Fprintln(w, indent+"╭"+bar+"╮")
	gap := strings.Repeat(" ", width-utf8.RuneCountInString(title)-len(version))
	fmt.Fprintln(w, indent+"│  "+st.bold(title)+gap+st.dim(version)+"  │")
	for i, l := range lines {
		text := padRight(l, width)
		if i == 3 {
			text = st.dim(text)
		}
		fmt.Fprintln(w, indent+"│  "+text+"  │")
	}
	fmt.Fprintln(w, indent+"╰"+bar+"╯")
	fmt.Fprintln(w)
}

func renderSaved(w io.Writer, st styles, tx store.Transaction, loc *time.Location, practice bool) {
	line := fmt.Sprintf("%s Saved %s %s · %s",
		st.green("✓"), st.kind(tx.Kind, string(tx.Kind)), money.Format(tx.Amount),
		tx.OccurredAt.In(loc).Format(timeLayout))
	if practice {
		line += "   " + st.dim("(practice, not saved)")
	}
	fmt.Fprintln(w, indent+line)
}

func renderError(w io.Writer, st styles, err error) {
	fmt.Fprintln(w, indent+st.red("✗")+" "+err.Error())
}

func renderMonth(w io.Writer, st styles, year int, month time.Month, txs []store.Transaction, loc *time.Location) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, indent+st.bold(fmt.Sprintf("%s %d", month, year)))
	if len(txs) == 0 {
		fmt.Fprintln(w, indent+st.dim("No transactions."))
		return
	}

	var income, expenses int64
	for _, tx := range txs {
		if tx.Kind == store.Income {
			income += tx.Amount
		} else {
			expenses += tx.Amount
		}
	}
	net := income - expenses

	numW := max(1, len(fmt.Sprint(len(txs))))
	const dateW, typeW = len(timeLayout), len("expense")
	amtW := len("Amount")
	for _, v := range []int64{income, expenses, net} {
		amtW = max(amtW, len(money.Format(v)))
	}
	for _, tx := range txs {
		amtW = max(amtW, len(money.Format(tx.Amount)))
	}

	sep := "   "
	rowW := numW + len(sep) + dateW + len(sep) + typeW + len(sep) + amtW
	rule := indent + st.dim(strings.Repeat("─", rowW+1))

	fmt.Fprintln(w, rule)
	fmt.Fprintln(w, indent+" "+st.dim(padRight("#", numW)+sep+padRight("Date", dateW)+sep+
		padRight("Type", typeW)+sep+padLeft("Amount", amtW)))
	for i, tx := range txs {
		fmt.Fprintln(w, indent+" "+
			padRight(fmt.Sprint(i+1), numW)+sep+
			tx.OccurredAt.In(loc).Format(timeLayout)+sep+
			st.kind(tx.Kind, padRight(string(tx.Kind), typeW))+sep+
			padLeft(money.Format(tx.Amount), amtW))
	}
	fmt.Fprintln(w, rule)

	labelW := rowW - amtW
	total := func(label string, value string) {
		fmt.Fprintln(w, indent+" "+padRight(label, labelW)+value)
	}
	total("Income", st.green(padLeft(money.Format(income), amtW)))
	total("Expenses", st.red(padLeft(money.Format(expenses), amtW)))
	total("Net", st.bold(st.signed(net, padLeft(money.Format(net), amtW))))
}

const helpText = `Commands
  income <amount> [YYYY-MM-DD HH:MM]     record money coming in
  expenses <amount> [YYYY-MM-DD HH:MM]   record money going out (or: expense)
  list [<month> <year>]                  show a month, e.g. list 10 2026
  edit [YYYY-MM-DD]                      change a saved entry: pick it from a day
  delete [YYYY-MM-DD]                    delete saved entries: pick them from a day
  trash                                  restore deleted entries (kept 30 days)
  undo                                   reverse the last edit, delete or restore
  /audio                                 speak entries in Indonesian, review, then save
                                         (say "stop" or "berhenti" to finish)
  /tutorial                              guided walkthrough (nothing is saved)
  /audio-status                          check if voice input (audio-to-text) can be used
  /setup-audio                           download the speech engine for voice input
  /help                                  show this help
  /exit                                  quit (or Ctrl+C)

Tab completes commands (in → income); type / to see them all.
Without a date, the current date and time are used.
Amounts: 50000   50000.50   5,000,000
Examples:
  income 5000000 2026-10-17 09:00
  expenses 25000`

func renderHelp(w io.Writer, st styles) {
	fmt.Fprintln(w)
	for i, l := range strings.Split(helpText, "\n") {
		if i == 0 {
			l = st.bold(l)
		}
		fmt.Fprintln(w, indent+l)
	}
}
