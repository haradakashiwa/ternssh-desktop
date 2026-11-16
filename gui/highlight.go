package gui

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/egoist/mygo/ui"
)

const (
	plainText = "纯文本"
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

// languages are the names a language can be chosen by, plain text first.
var languages = func() []string {
	names := lexers.Names(false)
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return append([]string{plainText}, names...)
}()

// setLanguage makes the highlighter lex as lang.
func (h *highlighter) setLanguage(lang string) {
	h.lang, h.lexer, h.ranges, h.version = lang, nil, nil, 0
	if lang != plainText {
		if l := lexers.Get(lang); l != nil {
			h.lexer = chroma.Coalesce(l)
		}
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
func lex(lexer chroma.Lexer, style *chroma.Style, text string) []ui.TextRange {
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return nil
	}
	base := style.Get(chroma.Text)
	total := utf8.RuneCountInString(text)
	var ranges []ui.TextRange
	pos := 0
	for tok := it(); tok != chroma.EOF; tok = it() {
		n := utf8.RuneCountInString(tok.Value)
		start, end := pos, min(pos+n, total)
		pos += n
		if end <= start {
			continue
		}
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
