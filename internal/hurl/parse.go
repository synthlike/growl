// Package hurl parses and runs a small subset of the Hurl file format
// (https://hurl.dev): requests with headers, [Query] and JSON or text bodies,
// and the HTTP status line with implicit header asserts, [Captures] and
// [Asserts]. Anything else in the
// format is rejected with an error naming it, never ignored.
package hurl

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Exit codes, as hurl uses them.
const (
	ExitParse   = 2
	ExitRuntime = 3
	ExitAssert  = 4
)

// Error is a problem at a 1-based line of the file. Code is the exit code
// it calls for.
type Error struct {
	Line int
	Msg  string
	Code int
}

func (e *Error) Error() string { return fmt.Sprintf("%d: %s", e.Line, e.Msg) }

func parseErr(line int, format string, args ...any) *Error {
	return &Error{Line: line, Msg: fmt.Sprintf(format, args...), Code: ExitParse}
}

// Entry is one request and the response it expects.
type Entry struct {
	Line     int // the request line, 1-based
	Method   string
	URL      string
	Headers  []Pair
	Query    []Pair
	Body     *Body
	Response *Response
}

// Pair is a header or parameter as written, with comments cut and escapes
// decoded, before templates are expanded.
type Pair struct {
	Line        int
	Name, Value string
}

// Body is a request body as written.
type Body struct {
	Line int
	JSON bool // a JSON object or array, or a ```json block: sent as application/json
	Text string
}

// Response is what an entry expects back.
type Response struct {
	Line     int
	Version  string // "" for any, else "1.0", "1.1", "2" or "3"
	Status   int    // 0 for *
	Headers  []Pair // implicit asserts: the header must have this value
	Captures []Capture
	Asserts  []Assert
}

var (
	methodRe  = regexp.MustCompile(`^([A-Z]+)\s+(\S+)$`)
	statusRe  = regexp.MustCompile(`^HTTP(?:/(1\.0|1\.1|2|3))?\s+([0-9]{3}|\*)$`)
	headerRe  = regexp.MustCompile("^([A-Za-z0-9!#$%&'*+.^_`|~-]+)\\s*:\\s*(.*)$")
	sectionRe = regexp.MustCompile(`^\[([A-Za-z]+)\]$`)
)

// sections are Hurl's section names; true marks the ones growl supports.
var sections = map[string]bool{
	"Query": true, "QueryStringParams": true,
	"Form": false, "FormParams": false, "Multipart": false, "MultipartFormData": false,
	"Cookies": false, "Options": false, "BasicAuth": false,
	"Asserts": false, "Captures": false,
}

type parser struct {
	lines []string
	i     int
}

// Parse reads a Hurl file.
func Parse(src string) ([]Entry, error) {
	p := &parser{lines: strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")}
	var es []Entry
	for {
		p.skip()
		if p.done() {
			return es, nil
		}
		e, err := p.entry()
		if err != nil {
			return nil, err
		}
		es = append(es, e)
	}
}

// Pick returns the index of the entry that holds line: the last one starting
// at or above it, or the first entry when line is above them all.
func Pick(es []Entry, line int) int {
	k := 0
	for i, e := range es {
		if e.Line <= line {
			k = i
		}
	}
	return k
}

func (p *parser) done() bool  { return p.i >= len(p.lines) }
func (p *parser) cur() string { return strings.TrimSpace(p.lines[p.i]) }
func (p *parser) num() int    { return p.i + 1 }

// skip moves past blank lines and comments.
func (p *parser) skip() {
	for !p.done() {
		if l := p.cur(); l != "" && !strings.HasPrefix(l, "#") {
			return
		}
		p.i++
	}
}

// section reports the known section name on the current line.
func (p *parser) section() (string, bool) {
	m := sectionRe.FindStringSubmatch(p.cur())
	if m == nil {
		return "", false
	}
	_, ok := sections[m[1]]
	return m[1], ok
}

func (p *parser) entry() (Entry, error) {
	m := methodRe.FindStringSubmatch(p.cur())
	if m == nil {
		return Entry{}, parseErr(p.num(), "want a request line like GET https://example.com, got %q", p.cur())
	}
	e := Entry{Line: p.num(), Method: m[1], URL: m[2]}
	p.i++
	hs, err := p.headers()
	if err != nil {
		return e, err
	}
	e.Headers = hs

	seen := map[string]bool{}
	for p.skip(); !p.done(); p.skip() {
		name, ok := p.section()
		if !ok {
			break
		}
		switch {
		case name == "Asserts" || name == "Captures":
			return e, parseErr(p.num(), "[%s] goes after the HTTP line", name)
		case !sections[name]:
			return e, parseErr(p.num(), "[%s] is not supported", name)
		}
		// Each section once per request, as in hurl; aliases count as one.
		kind := map[string]string{"QueryStringParams": "Query", "Form": "FormParams"}[name]
		if kind == "" {
			kind = name
		}
		if seen[kind] {
			return e, parseErr(p.num(), "[%s] appears twice in this request", name)
		}
		seen[kind] = true
		p.i++
		ps, err := p.params()
		if err != nil {
			return e, err
		}
		e.Query = ps
	}

	if p.skip(); !p.done() && p.bodyStart() {
		b, err := p.body()
		if err != nil {
			return e, err
		}
		e.Body = b
	}

	if p.skip(); !p.done() {
		if m := statusRe.FindStringSubmatch(p.cur()); m != nil {
			r := &Response{Line: p.num(), Version: m[1]}
			if m[2] != "*" {
				fmt.Sscan(m[2], &r.Status)
			}
			p.i++
			if r.Headers, err = p.headers(); err != nil {
				return e, err
			}
			e.Response = r
			if err := p.responseSections(r); err != nil {
				return e, err
			}
			if p.skip(); !p.done() {
				if p.bodyStart() {
					return e, parseErr(p.num(), "response body asserts are not supported yet")
				}
			}
		}
	}

	if p.skip(); !p.done() && !methodRe.MatchString(p.cur()) {
		return e, parseErr(p.num(), "unexpected %q (want a header, section, body, HTTP line or the next request)", p.cur())
	}
	return e, nil
}

// headers reads Name: value lines.
func (p *parser) headers() ([]Pair, error) {
	var hs []Pair
	for p.skip(); !p.done(); p.skip() {
		m := headerRe.FindStringSubmatch(p.cur())
		if m == nil {
			break
		}
		v, err := value(m[2], p.num())
		if err != nil {
			return nil, err
		}
		hs = append(hs, Pair{Line: p.num(), Name: m[1], Value: v})
		p.i++
	}
	return hs, nil
}

// params reads the key: value lines of a section.
func (p *parser) params() ([]Pair, error) {
	var ps []Pair
	for p.skip(); !p.done(); p.skip() {
		l := p.cur()
		pr, err := param(l, p.num())
		if strings.HasPrefix(l, "{{") && err == nil {
			// {{name}}: value is a param, not a JSON body.
		} else if _, ok := p.section(); ok || methodRe.MatchString(l) || statusRe.MatchString(l) || p.bodyStart() {
			break
		}
		if err != nil {
			return nil, err
		}
		ps = append(ps, pr)
		p.i++
	}
	return ps, nil
}

// param splits a key: value line. Keys hold letters, digits, _-.[]@$ and
// templates, as in hurl; growl doesn't take hurl's escapes in keys.
func param(l string, line int) (Pair, error) {
	i := 0
	for i < len(l) {
		if strings.HasPrefix(l[i:], "{{") {
			end := strings.Index(l[i:], "}}")
			if end < 0 {
				break
			}
			i += end + 2
			continue
		}
		c, n := utf8.DecodeRuneInString(l[i:])
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune("_-.[]@$", c) {
			break
		}
		i += n
	}
	key, rest := l[:i], strings.TrimLeft(l[i:], " \t")
	switch {
	case key != "" && strings.HasPrefix(rest, ":"):
	case key != "" && i < len(l) && l[i] == '\\':
		return Pair{}, parseErr(line, "escapes in keys are not supported")
	default:
		return Pair{}, parseErr(line, "want key: value, got %q", l)
	}
	v, err := value(rest[1:], line)
	if err != nil {
		return Pair{}, err
	}
	return Pair{Line: line, Name: key, Value: v}, nil
}

// value reads a header or parameter value as hurl does: an unescaped # starts
// a comment, and \" \\ \# \b \f \n \r \t and \u{hex} are escapes.
func value(s string, line int) (string, error) {
	s = strings.TrimLeft(s, " \t")
	var b strings.Builder
	keep := 0 // the length without trailing unescaped spaces
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '#' {
			break
		}
		if c != '\\' {
			b.WriteByte(c)
			if c != ' ' && c != '\t' {
				keep = b.Len()
			}
			continue
		}
		i++
		if i == len(s) {
			return "", parseErr(line, "a value can't end with \\")
		}
		switch s[i] {
		case '"', '\\', '#':
			b.WriteByte(s[i])
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			end := strings.IndexByte(s[i:], '}')
			if !strings.HasPrefix(s[i:], "u{") || end < 0 {
				return "", parseErr(line, "want \\u{hex}")
			}
			n, err := strconv.ParseUint(s[i+2:i+end], 16, 32)
			if err != nil || n > unicode.MaxRune {
				return "", parseErr(line, "bad escape \\%s", s[i:i+end+1])
			}
			b.WriteRune(rune(n))
			i += end
		default:
			return "", parseErr(line, "unknown escape \\%c", s[i])
		}
		keep = b.Len()
	}
	return b.String()[:keep], nil
}

// bodyStart reports whether the current line starts a body.
func (p *parser) bodyStart() bool {
	l := p.cur()
	if _, ok := p.section(); ok {
		return false
	}
	for _, s := range []string{"{", "[", "`", "<", "base64,", "hex,", "file,"} {
		if strings.HasPrefix(l, s) {
			return true
		}
	}
	return false
}

func (p *parser) body() (*Body, error) {
	l, start := p.cur(), p.num()
	switch {
	case strings.HasPrefix(l, "```"):
		lang := strings.TrimSpace(l[3:])
		if lang != "" && lang != "json" {
			return nil, parseErr(start, "```%s bodies are not supported; use ``` or ```json", lang)
		}
		p.i++
		var text strings.Builder
		for ; !p.done(); p.i++ {
			if p.cur() == "```" {
				p.i++
				return &Body{Line: start, JSON: lang == "json", Text: text.String()}, nil
			}
			text.WriteString(p.lines[p.i])
			text.WriteByte('\n')
		}
		return nil, parseErr(start, "unterminated ``` body")
	case strings.HasPrefix(l, "`"):
		if len(l) < 2 || !strings.HasSuffix(l, "`") {
			return nil, parseErr(start, "unterminated `oneline` body")
		}
		p.i++
		return &Body{Line: start, Text: l[1 : len(l)-1]}, nil
	case strings.HasPrefix(l, "{"), strings.HasPrefix(l, "["):
		return p.json()
	case strings.HasPrefix(l, "<"):
		return nil, parseErr(start, "XML bodies are not supported")
	default:
		return nil, parseErr(start, "%s bodies are not supported", strings.SplitN(l, ",", 2)[0])
	}
}

// json reads a JSON body: lines until its brackets balance.
func (p *parser) json() (*Body, error) {
	start := p.num()
	depth, inStr, esc := 0, false, false
	var text strings.Builder
	for ; !p.done(); p.i++ {
		line := p.lines[p.i]
		text.WriteString(line)
		text.WriteByte('\n')
		for _, c := range line {
			switch {
			case esc:
				esc = false
			case inStr && c == '\\':
				esc = true
			case c == '"':
				inStr = !inStr
			case inStr:
			case c == '{' || c == '[':
				depth++
			case c == '}' || c == ']':
				depth--
			}
		}
		if depth == 0 && !inStr {
			p.i++
			// The body ends at its closing bracket, as in hurl.
			return &Body{Line: start, JSON: true, Text: strings.TrimRight(text.String(), " \t\n")}, nil
		}
	}
	return nil, parseErr(start, "unterminated JSON body")
}

// responseSections reads the sections after the HTTP line.
func (p *parser) responseSections(r *Response) error {
	for p.skip(); !p.done(); p.skip() {
		name, ok := p.section()
		if !ok {
			return nil
		}
		switch name {
		case "Asserts", "Captures":
		default:
			return parseErr(p.num(), "[%s] belongs to the request, before the HTTP line", name)
		}
		p.i++
		for p.skip(); !p.done(); p.skip() {
			if _, ok := p.section(); ok || methodRe.MatchString(p.cur()) || p.bodyStart() {
				break
			}
			if name == "Captures" {
				c, err := parseCapture(p.cur(), p.num())
				if err != nil {
					return err
				}
				r.Captures = append(r.Captures, c)
			} else {
				a, err := parseAssert(p.cur(), p.num())
				if err != nil {
					return err
				}
				r.Asserts = append(r.Asserts, a)
			}
			p.i++
		}
	}
	return nil
}
