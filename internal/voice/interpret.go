// Package voice turns speech into entries: the Indonesian transcript interpreter and the
// listening session that connects the microphone, the segmenter and the speech engine.
package voice

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"money-manager/internal/money"
	"money-manager/internal/store"
)

// Draft is a recognised entry waiting in memory for the user's review.
type Draft struct {
	Kind     store.Kind
	Amount   int64     // cents
	At       time.Time // when the money moved: recording start unless the user changed it
	Recorded time.Time // when recording of this entry started
	Heard    string    // transcript, shown in the review
}

// ErrNothingHeard means the transcript had no words (silence, noise, or a known
// Whisper filler such as "[BLANK_AUDIO]"). Callers usually ignore it quietly.
var ErrNothingHeard = errors.New("nothing heard")

// Interpret turns a transcript like "income dua ratus ribu" into a Draft at time at.
func Interpret(text string, at time.Time) (Draft, error) {
	toks := tokenize(text)
	if len(toks) == 0 || isFiller(toks) {
		return Draft{}, ErrNothingHeard
	}
	for len(toks) > 0 && fillerWords[toks[0]] {
		toks = toks[1:]
	}
	if len(toks) == 0 {
		return Draft{}, ErrNothingHeard
	}

	kind, n := commandWord(toks)
	if n == 0 {
		return Draft{}, fmt.Errorf(`no income/expenses word. Start with "income" or "pendapatan", "expenses" or "pengeluaran"`)
	}
	rest := toks[n:]
	if len(rest) == 0 {
		return Draft{}, fmt.Errorf("no amount heard after %q", strings.Join(toks[:n], " "))
	}

	p := &amountParser{toks: rest}
	cents, err := p.parse()
	if err != nil {
		return Draft{}, err
	}
	if cents <= 0 {
		return Draft{}, errors.New("the amount must be more than zero")
	}
	if cents > money.MaxCents {
		return Draft{}, errors.New("the amount is too large")
	}
	return Draft{Kind: kind, Amount: cents, At: at, Recorded: at, Heard: strings.TrimSpace(text)}, nil
}

// IsStop reports whether the transcript is only a stop command: "stop" or "berhenti"
// (also the Indonesian spelling "setop" and colloquial "berenti"), said on its own.
func IsStop(text string) bool {
	toks := tokenize(text)
	for len(toks) > 0 && fillerWords[toks[0]] {
		toks = toks[1:]
	}
	if len(toks) == 0 || len(toks) > 2 {
		return false
	}
	for _, t := range toks {
		switch {
		case t == "stop" || t == "setop" || t == "stopp" || t == "berenti":
		case levenshtein(t, "berhenti") <= 1:
		default:
			return false
		}
	}
	return true
}

// tokenize lowercases, splits on spaces and hyphens, strips punctuation around words,
// and splits a "Rp" prefix from digits ("Rp200.000" → "rp", "200.000").
func tokenize(text string) []string {
	text = strings.ToLower(strings.NewReplacer("-", " ", "_", " ").Replace(text))
	var toks []string
	for _, f := range strings.Fields(text) {
		f = strings.TrimFunc(f, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if f == "" {
			continue
		}
		if strings.HasPrefix(f, "rp") && len(f) > 2 && f[2] >= '0' && f[2] <= '9' {
			toks = append(toks, "rp")
			f = f[2:]
		}
		toks = append(toks, f)
	}
	return toks
}

var fillerWords = map[string]bool{"eh": true, "em": true, "emm": true, "hmm": true, "oke": true, "ok": true, "ya": true, "nah": true}

// isFiller recognises transcripts Whisper produces for silence or noise.
func isFiller(toks []string) bool {
	switch strings.Join(toks, " ") {
	case "blank audio", "musik", "music", "terima kasih", "terima kasih telah menonton", "suara", "silence":
		return true
	}
	return false
}

var commandVariants = []struct {
	kind  store.Kind
	words []string
}{
	{store.Income, []string{"income", "inkam", "inkom", "inkem", "incom", "pendapatan"}},
	{store.Expense, []string{"expenses", "expense", "expens", "ekspens", "ekspenses", "expanses", "pengeluaran"}},
}

// commandWord matches the first word (or the first two joined, for "in come") against the
// command words, allowing small spelling differences. It returns the number of tokens used.
func commandWord(toks []string) (store.Kind, int) {
	try := func(w string) (store.Kind, bool) {
		for _, c := range commandVariants {
			for _, v := range c.words {
				// Longer words tolerate more slips; pendapatan and pengeluaran
				// are 6+ edits apart, so they can't be confused with each other.
				limit := 1
				switch {
				case len(v) >= 10:
					limit = 3
				case len(v) >= 7:
					limit = 2
				}
				if levenshtein(w, v) <= limit {
					return c.kind, true
				}
			}
		}
		return "", false
	}
	if k, ok := try(toks[0]); ok {
		return k, 1
	}
	if len(toks) > 1 {
		if k, ok := try(toks[0] + toks[1]); ok {
			return k, 2
		}
	}
	return "", 0
}

func levenshtein(a, b string) int {
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

var units = map[string]int64{
	"satu": 1, "dua": 2, "tiga": 3, "empat": 4, "lima": 5,
	"enam": 6, "tujuh": 7, "delapan": 8, "sembilan": 9,
}

var scales = map[string]int64{
	"ribu": 1_000, "juta": 1_000_000, "miliar": 1_000_000_000, "milyar": 1_000_000_000,
	"triliun": 1_000_000_000_000,
}

// "seribu" = one thousand, etc.
var seScales = map[string]int64{
	"seribu": 1_000, "sejuta": 1_000_000, "semiliar": 1_000_000_000, "semilyar": 1_000_000_000,
	"setriliun": 1_000_000_000_000,
}

// amountParser reads an Indonesian amount: number words ("dua ratus lima puluh ribu"),
// digits with Indonesian separators ("200.000", "2,5"), "setengah", "koma", scales, and
// an optional "rp"/"rupiah". Words after a complete amount are ignored.
type amountParser struct {
	toks []string
	i    int
}

func (p *amountParser) peek(k int) string {
	if p.i+k < len(p.toks) {
		return p.toks[p.i+k]
	}
	return ""
}

func (p *amountParser) parse() (int64, error) {
	if p.peek(0) == "rp" {
		p.i++
	}
	var total int64 // cents
	lastScale := int64(-1)
	found := false

	for p.i < len(p.toks) {
		if s, ok := seScales[p.peek(0)]; ok { // "seribu", "sejuta", ...
			p.i++
			if err := p.addScale(&total, &lastScale, 100, s); err != nil {
				return 0, err
			}
			found = true
			continue
		}

		seg, has, err := p.number()
		if err != nil {
			return 0, err
		}
		if p.peek(0) == "setengah" {
			seg, has = seg+50, true
			p.i++
		}
		if has && p.peek(0) == "koma" {
			p.i++
			frac, err := p.decimals()
			if err != nil {
				return 0, err
			}
			seg += frac
		}

		if s, ok := scales[p.peek(0)]; ok {
			if !has {
				return 0, fmt.Errorf("couldn't understand the amount: %q needs a number before it", p.peek(0))
			}
			p.i++
			if err := p.addScale(&total, &lastScale, seg, s); err != nil {
				return 0, err
			}
			found = true
			continue
		}
		if !has {
			break // not part of the amount
		}
		// A group without a scale ends the amount ("dua juta lima ratus").
		total += seg
		found = true
		if next := p.peek(0); units[next] != 0 || isNumeral(next) || next == "nol" {
			return 0, fmt.Errorf("couldn't understand the amount %q", strings.Join(p.toks, " "))
		}
		break
	}
	if !found {
		return 0, fmt.Errorf("no amount heard in %q", strings.Join(p.toks, " "))
	}
	// Words after the amount are ignored, unless one looks like a misheard number word
	// ("dua ratus rebu"): then the amount is probably wrong, so refuse to guess.
	for _, w := range p.toks[p.i:] {
		if looksLikeNumberWord(w) {
			return 0, fmt.Errorf("couldn't understand the amount %q (%q isn't a number word)", strings.Join(p.toks, " "), w)
		}
	}
	return total, nil
}

func looksLikeNumberWord(w string) bool {
	if w == "rupiah" {
		return false
	}
	for _, n := range []string{"ribu", "juta", "miliar", "milyar", "triliun", "ratus", "puluh", "belas", "setengah", "koma", "seribu", "sejuta", "seratus"} {
		if levenshtein(w, n) <= 1 {
			return true
		}
	}
	return false
}

func (p *amountParser) addScale(total, lastScale *int64, seg, scale int64) error {
	if *lastScale != -1 && scale >= *lastScale {
		return fmt.Errorf("couldn't understand the amount %q", strings.Join(p.toks, " "))
	}
	*lastScale = scale
	*total += seg * scale
	return nil
}

// number reads one group below a thousand in words, or one numeral. Result in cents.
func (p *amountParser) number() (int64, bool, error) {
	if isNumeral(p.peek(0)) {
		v, err := parseNumeral(p.peek(0))
		if err != nil {
			return 0, false, err
		}
		p.i++
		return v, true, nil
	}
	v, ok := p.group()
	return v * 100, ok, nil
}

// group parses [hundreds] [tens] [unit], e.g. "dua ratus lima puluh tiga", in rupiah.
func (p *amountParser) group() (int64, bool) {
	var v int64
	found := false
	unitThen := func(word string) (int64, bool) {
		u, ok := units[p.peek(0)]
		if ok && p.peek(1) == word {
			p.i += 2
			return u, true
		}
		return 0, false
	}

	if p.peek(0) == "seratus" {
		v, found = 100, true
		p.i++
	} else if u, ok := unitThen("ratus"); ok {
		v, found = u*100, true
	}

	switch {
	case p.peek(0) == "sepuluh":
		v, found = v+10, true
		p.i++
		return v, found
	case p.peek(0) == "sebelas":
		v, found = v+11, true
		p.i++
		return v, found
	}
	if u, ok := unitThen("belas"); ok {
		return v + 10 + u, true
	}
	if u, ok := unitThen("puluh"); ok {
		v, found = v+u*10, true
	}
	if u, ok := units[p.peek(0)]; ok && p.peek(1) != "ratus" && p.peek(1) != "puluh" && p.peek(1) != "belas" {
		v, found = v+u, true
		p.i++
	}
	if !found && p.peek(0) == "nol" {
		p.i++
		return 0, true
	}
	return v, found
}

// decimals reads the digits after "koma": spoken digit words or a numeral.
func (p *amountParser) decimals() (int64, error) {
	var digits []int64
	for len(digits) < 3 {
		w := p.peek(0)
		if u, ok := units[w]; ok {
			digits = append(digits, u)
		} else if w == "nol" {
			digits = append(digits, 0)
		} else if isNumeral(w) && !strings.ContainsAny(w, ".,") {
			for _, c := range w {
				digits = append(digits, int64(c-'0'))
			}
		} else {
			break
		}
		p.i++
	}
	if len(digits) == 0 {
		return 0, errors.New(`couldn't understand the amount: no digits after "koma"`)
	}
	if len(digits) > 2 {
		return 0, errors.New("the amount has too many decimals")
	}
	cents := digits[0] * 10
	if len(digits) == 2 {
		cents += digits[1]
	}
	return cents, nil
}

func isNumeral(s string) bool {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && c != '.' && c != ',' {
			return false
		}
	}
	return true
}

// parseNumeral reads digits written by the speech engine. A separator followed by exactly
// three digits groups thousands ("200.000", "200,000"); a final separator followed by one
// or two digits is the decimal point ("2,5", "1.5", "200.000,00"). Result in cents.
func parseNumeral(s string) (int64, error) {
	bad := fmt.Errorf("couldn't understand the number %q", s)
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == ',' })
	if len(parts) == 0 || strings.Count(s, ".")+strings.Count(s, ",") != len(parts)-1 {
		return 0, bad
	}
	frac := ""
	if last := parts[len(parts)-1]; len(parts) > 1 && len(last) <= 2 {
		frac = last
		parts = parts[:len(parts)-1]
	}
	for _, g := range parts[1:] {
		if len(g) != 3 {
			return 0, bad
		}
	}
	whole := strings.Join(parts, "")
	if len(strings.TrimLeft(whole, "0")) > 15 {
		return 0, errors.New("the amount is too large")
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, bad
	}
	cents := w * 100
	if frac != "" {
		f, _ := strconv.ParseInt(frac, 10, 64)
		if len(frac) == 1 {
			f *= 10
		}
		cents += f
	}
	return cents, nil
}
