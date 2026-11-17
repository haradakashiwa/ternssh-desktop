package gui

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/egoist/mygo/ui"

	"ternssh-desktop/locales"
)

const (
	// plainText is the language of text not highlighted, which shows as
	// editorPlainText; no lexer is named "".
	plainText = ""
	// Text past highlightMax is not highlighted, and text past lexInline
	// is lexed away from the frame.
	highlightMax = 1 << 20
	lexInline    = 256 << 10
)

// highlighter colors an editor's text by its language.
type highlighter struct {
	lang    string // the lexer's name, or plainText
	lexer   chroma.Lexer
	version uint64 // the buffer's version ranges are, or are being, lexed for
	dark    bool
	lexing  bool
	ranges  []ui.TextRange
	options []string // languageOptions
}

// detectLanguage returns the language of a file, by its name, or else by
// how it begins (a #! line).
func detectLanguage(name, text string) string {
	l := lexers.Match(name)
	if l == nil {
		head := text
		if len(head) > 4096 {
			head = head[:4096]
		}
		l = lexers.Analyse(head)
	}
	if l == nil {
		return plainText
	}
	return l.Config().Name
}

// lexerNames are the names a language can be chosen by, plain text aside.
var lexerNames = func() []string {
	names := lexers.Names(false)
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names
}()

// languageOptions are the choices of the editor's language: plain text,
// by its label in the language shown, then the lexers' names. They are
// kept until the language shown changes.
func (h *highlighter) languageOptions() []string {
	if label := locales.EditorPlainText(); len(h.options) == 0 || h.options[0] != label {
		h.options = append([]string{label}, lexerNames...)
	}
	return h.options
}

// setLanguage makes the highlighter lex as lang.
func (h *highlighter) setLanguage(lang string) {
	h.lang, h.lexer, h.ranges, h.version = lang, nil, nil, 0
	if lang != plainText {
		// Not coalesced: a token not in the text, which lex leaves out,
		// would take the next with it. lex joins runs of a style itself.
		h.lexer = lexers.Get(lang)
	}
}

// styleFor is the colors of the code: GitHub's, light or dark.
func styleFor(dark bool) *chroma.Style {
	if dark {
		return styles.Get("github-dark")
	}
	return styles.Get("github")
}

// lex returns the ranges of text to color, in runes, as style colors them.
//
// The tokens are found in the text rather than counted: a lexer's tokens
// need not spell it out, as chroma's own CRLF to LF, or its YAML lexer's
// indent of a block scalar given twice, and a count would shift every
// range after.
func lex(lexer chroma.Lexer, style *chroma.Style, text string) []ui.TextRange {
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return nil
	}
	base := style.Get(chroma.Text)
	var ranges []ui.TextRange
	at, pos := 0, 0 // in bytes and runes
	for tok := it(); tok != chroma.EOF; tok = it() {
		end, n, ok := follow(text, at, tok.Value)
		if !ok {
			continue // not in the text
		}
		start := pos
		at, pos = end, pos+n
		if pos <= start {
			continue
		}
		end = pos
		entry := style.Get(tok.Type)
		bold := entry.Bold == chroma.Yes
		if !entry.Colour.IsSet() || (entry.Colour == base.Colour && !bold) {
			continue
		}
		r := ui.TextRange{Start: start, End: end, Color: ui.RGB(entry.Colour.Red(), entry.Colour.Green(), entry.Colour.Blue())}
		if bold {
			r.Weight = 700
		}
		// Runs of one style, as a word and the space after it, are one.
		if k := len(ranges) - 1; k >= 0 && ranges[k].End == start && ranges[k].Color == r.Color && ranges[k].Weight == r.Weight {
			ranges[k].End = end
			continue
		}
		ranges = append(ranges, r)
	}
	return ranges
}

// follow reports whether text from byte at goes on with value, but for the
// CRs before its LFs a lexer drops, and where it ends then, in bytes, and
// how many runes it takes.
func follow(text string, at int, value string) (end, runes int, ok bool) {
	i := at
	for j := 0; j < len(value); {
		switch {
		case i < len(text) && text[i] == value[j]:
			i, j = i+1, j+1
		case i+1 < len(text) && text[i] == '\r' && text[i+1] == '\n' && value[j] == '\n':
			i++
		default:
			return at, 0, false
		}
	}
	return i, utf8.RuneCountInString(text[at:i]), true
}

// highlight returns the ranges of an editor's text, lexing it again as it
// changes: in the frame while it is short, else away from it, while the
// ranges before show.
func (e *Editor) highlight(dark bool) []ui.TextRange {
	h := &e.hl
	if h.lexer == nil {
		return nil
	}
	v := e.buf.Version()
	if (h.version == v && h.dark == dark) || h.lexing {
		return h.ranges
	}
	if e.buf.ByteLen() > highlightMax {
		h.ranges, h.version, h.dark = nil, v, dark
		return nil
	}
	text, lexer, style := e.buf.String(), h.lexer, styleFor(dark)
	h.version, h.dark = v, dark
	if len(text) <= lexInline {
		h.ranges = lex(lexer, style, text)
		return h.ranges
	}
	h.lexing = true
	go func() {
		ranges := lex(lexer, style, text)
		e.update(func() {
			h.lexing = false
			if h.lexer == lexer && e.buf.Version() == v {
				h.ranges = ranges
			}
		})
	}()
	return h.ranges
}
