// Package hurl parses and runs a small subset of the Hurl file format
// (https://hurl.dev): requests with headers, [Query] and JSON or text bodies,
// and the HTTP status line with implicit header asserts and [Asserts]. Anything else in the
// format is rejected with an error naming it, never ignored.
package hurl

import (
	"fmt"
	"regexp"
	"strings"
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

// Pair is a header or parameter as written, before templates are expanded.
type Pair struct {
	Line        int
	Name, Value string
}

// Body is a request body as written.
type Body struct {
	Line int
	JSON bool // written as a JSON object or array, not a text block
	Text string
}

// Response is what an entry expects back.
type Response struct {
	Line    int
	Version string // "" for any, else "1.0", "1.1", "2" or "3"
	Status  int    // 0 for *
	Headers []Pair // implicit asserts: the header must have this value
	Asserts []Assert
}

var (
	methodRe  = regexp.MustCompile(`^([A-Z]+)\s+(\S+)$`)
	statusRe  = regexp.MustCompile(`^HTTP(?:/(1\.0|1\.1|2|3))?\s+([0-9]{3}|\*)$`)
	headerRe  = regexp.MustCompile("^([A-Za-z0-9!#$%&'*+.^_`|~-]+)\\s*:\\s*(.*)$")
	paramRe   = regexp.MustCompile(`^([^:\s][^:]*?)\s*:\s*(.*)$`)
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
	e.Headers = p.headers()

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
		p.i++
		ps, err := p.params()
		if err != nil {
			return e, err
		}
		e.Query = append(e.Query, ps...)
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
			r.Headers = p.headers()
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
func (p *parser) headers() []Pair {
	var hs []Pair
	for p.skip(); !p.done(); p.skip() {
		m := headerRe.FindStringSubmatch(p.cur())
		if m == nil {
			break
		}
		hs = append(hs, Pair{Line: p.num(), Name: m[1], Value: m[2]})
		p.i++
	}
	return hs
}

// params reads the key: value lines of a section.
func (p *parser) params() ([]Pair, error) {
	var ps []Pair
	for p.skip(); !p.done(); p.skip() {
		l := p.cur()
		if _, ok := p.section(); ok || methodRe.MatchString(l) || statusRe.MatchString(l) || p.bodyStart() {
			break
		}
		m := paramRe.FindStringSubmatch(l)
		if m == nil {
			return nil, parseErr(p.num(), "want key: value, got %q", l)
		}
		ps = append(ps, Pair{Line: p.num(), Name: m[1], Value: m[2]})
		p.i++
	}
	return ps, nil
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
				return &Body{Line: start, Text: text.String()}, nil
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
			return &Body{Line: start, JSON: true, Text: text.String()}, nil
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
		case "Asserts":
		case "Captures":
			return parseErr(p.num(), "[Captures] is not supported yet")
		default:
			return parseErr(p.num(), "[%s] belongs to the request, before the HTTP line", name)
		}
		p.i++
		for p.skip(); !p.done(); p.skip() {
			if _, ok := p.section(); ok || methodRe.MatchString(p.cur()) || p.bodyStart() {
				break
			}
			a, err := parseAssert(p.cur(), p.num())
			if err != nil {
				return err
			}
			r.Asserts = append(r.Asserts, a)
			p.i++
		}
	}
	return nil
}
