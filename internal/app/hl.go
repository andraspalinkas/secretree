package app

import (
	"html/template"
	"path"
	"strings"
	"unicode"
)

// A tiny syntax highlighter: comments, strings, numbers and keywords for
// the languages people keep in git. It is line-oriented with a carried
// "inside block comment" state, produces escaped HTML spans, and never
// touches the text otherwise. Good enough to read code; not a parser.

type hlLang struct {
	line     []string // line comment starters
	block    [2]string
	strings  []byte // quote characters
	keywords map[string]bool
}

func kw(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

var (
	hlGo = &hlLang{line: []string{"//"}, block: [2]string{"/*", "*/"}, strings: []byte{'"', '\'', '`'},
		keywords: kw("break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false iota bool byte error int int8 int16 int32 int64 uint uint8 uint16 uint32 uint64 uintptr float32 float64 complex64 complex128 string rune any make new len cap append copy delete panic recover")}
	hlJS = &hlLang{line: []string{"//"}, block: [2]string{"/*", "*/"}, strings: []byte{'"', '\'', '`'},
		keywords: kw("async await break case catch class const continue debugger default delete do else enum export extends false finally for from function if implements import in instanceof interface let new null of package private protected public return static super switch this throw true try type typeof undefined var void while with yield readonly declare namespace abstract as satisfies keyof")}
	hlPy = &hlLang{line: []string{"#"}, strings: []byte{'"', '\''},
		keywords: kw("False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield self print")}
	hlSh = &hlLang{line: []string{"#"}, strings: []byte{'"', '\''},
		keywords: kw("if then else elif fi for while until do done case esac in function return exit export local set unset readonly shift echo printf test source cd true false")}
	hlRs = &hlLang{line: []string{"//"}, block: [2]string{"/*", "*/"}, strings: []byte{'"'},
		keywords: kw("as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while Some None Ok Err Box Vec String Option Result")}
	hlC = &hlLang{line: []string{"//"}, block: [2]string{"/*", "*/"}, strings: []byte{'"', '\''},
		keywords: kw("auto break case char const continue default do double else enum extern float for goto if inline int long register restrict return short signed sizeof static struct switch typedef union unsigned void volatile while class namespace template typename public private protected virtual override new delete this nullptr true false bool using try catch throw")}
	hlJava = &hlLang{line: []string{"//"}, block: [2]string{"/*", "*/"}, strings: []byte{'"', '\''},
		keywords: kw("abstract assert boolean break byte case catch char class const continue default do double else enum extends final finally float for if implements import instanceof int interface long native new package private protected public return short static strictfp super switch synchronized this throw throws transient try void volatile while true false null var record sealed permits fun val when object data")}
	hlRuby = &hlLang{line: []string{"#"}, strings: []byte{'"', '\''},
		keywords: kw("alias and begin break case class def defined? do else elsif end ensure false for if in module next nil not or redo rescue retry return self super then true undef unless until when while yield require attr_accessor puts")}
	hlYAML = &hlLang{line: []string{"#"}, strings: []byte{'"', '\''}, keywords: kw("true false null yes no on off")}
	hlJSON = &hlLang{strings: []byte{'"'}, keywords: kw("true false null")}
	hlCSS  = &hlLang{block: [2]string{"/*", "*/"}, strings: []byte{'"', '\''}}
	hlSQL  = &hlLang{line: []string{"--"}, block: [2]string{"/*", "*/"}, strings: []byte{'\''},
		keywords: kw("select from where insert into values update set delete create table index view drop alter add primary key foreign references not null unique default and or in is like between join left right inner outer on group by order having limit offset as distinct union all exists case when then else end begin commit rollback transaction SELECT FROM WHERE INSERT INTO VALUES UPDATE SET DELETE CREATE TABLE INDEX VIEW DROP ALTER ADD PRIMARY KEY FOREIGN REFERENCES NOT NULL UNIQUE DEFAULT AND OR IN IS LIKE BETWEEN JOIN LEFT RIGHT INNER OUTER ON GROUP BY ORDER HAVING LIMIT OFFSET AS DISTINCT UNION ALL EXISTS CASE WHEN THEN ELSE END BEGIN COMMIT ROLLBACK TRANSACTION")}
)

func hlFor(name string) *hlLang {
	base := strings.ToLower(path.Base(name))
	switch base {
	case "makefile", "gnumakefile", "dockerfile", ".gitignore", ".env":
		return hlSh
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".go":
		return hlGo
	case ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".vue", ".svelte":
		return hlJS
	case ".py", ".pyi":
		return hlPy
	case ".sh", ".bash", ".zsh", ".fish", ".ci", ".toml", ".ini", ".cfg", ".conf":
		return hlSh
	case ".rs":
		return hlRs
	case ".c", ".h", ".cc", ".cpp", ".hpp", ".cxx", ".m", ".mm", ".swift", ".cs":
		return hlC
	case ".java", ".kt", ".kts", ".scala", ".groovy":
		return hlJava
	case ".rb", ".rake", ".gemspec":
		return hlRuby
	case ".yml", ".yaml":
		return hlYAML
	case ".json", ".jsonc", ".json5":
		return hlJSON
	case ".css", ".scss", ".less":
		return hlCSS
	case ".sql":
		return hlSQL
	}
	return nil
}

// highlighter keeps block-comment state between lines.
type highlighter struct {
	lang    *hlLang
	inBlock bool
}

func newHighlighter(name string) *highlighter { return &highlighter{lang: hlFor(name)} }

func esc(s string) string { return template.HTMLEscapeString(s) }

// Line returns the escaped, span-wrapped HTML of one source line.
func (h *highlighter) Line(s string) template.HTML {
	if h.lang == nil {
		return template.HTML(esc(s))
	}
	var b strings.Builder
	i := 0
	n := len(s)
	flushWord := func(w string) {
		if w == "" {
			return
		}
		switch {
		case h.lang.keywords[w]:
			b.WriteString(`<span class="kw">` + esc(w) + `</span>`)
		case isNumber(w):
			b.WriteString(`<span class="num">` + esc(w) + `</span>`)
		default:
			b.WriteString(esc(w))
		}
	}
	for i < n {
		if h.inBlock {
			end := strings.Index(s[i:], h.lang.block[1])
			if end < 0 {
				b.WriteString(`<span class="cm">` + esc(s[i:]) + `</span>`)
				return template.HTML(b.String())
			}
			b.WriteString(`<span class="cm">` + esc(s[i:i+end+len(h.lang.block[1])]) + `</span>`)
			i += end + len(h.lang.block[1])
			h.inBlock = false
			continue
		}
		// block comment start
		if h.lang.block[0] != "" && strings.HasPrefix(s[i:], h.lang.block[0]) {
			h.inBlock = true
			continue
		}
		// line comment
		lc := false
		for _, lcs := range h.lang.line {
			if strings.HasPrefix(s[i:], lcs) {
				lc = true
			}
		}
		if lc {
			b.WriteString(`<span class="cm">` + esc(s[i:]) + `</span>`)
			return template.HTML(b.String())
		}
		c := s[i]
		// string
		if isQuote(h.lang, c) {
			j := i + 1
			for j < n && s[j] != c {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= n {
				j = n - 1
			}
			b.WriteString(`<span class="str">` + esc(s[i:j+1]) + `</span>`)
			i = j + 1
			continue
		}
		// word
		if isWordStart(rune(c)) {
			j := i + 1
			for j < n && isWordChar(rune(s[j])) {
				j++
			}
			flushWord(s[i:j])
			i = j
			continue
		}
		b.WriteString(esc(string(c)))
		i++
	}
	return template.HTML(b.String())
}

func isQuote(l *hlLang, c byte) bool {
	for _, q := range l.strings {
		if q == c {
			return true
		}
	}
	return false
}

func isWordStart(r rune) bool { return unicode.IsLetter(r) || r == '_' || unicode.IsDigit(r) }
func isWordChar(r rune) bool  { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

func isNumber(w string) bool {
	if w == "" || !unicode.IsDigit(rune(w[0])) {
		return false
	}
	for _, r := range w {
		if !(unicode.IsDigit(r) || r == '.' || r == 'x' || r == 'X' || r == '_' || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
