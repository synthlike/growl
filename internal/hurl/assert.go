package hurl

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Assert is one line of an [Asserts] section:
//
//	query [filter] [not] predicate [value]
type Assert struct {
	Line  int
	Text  string // as written, for messages
	Query string // status, header, jsonpath, body or duration
	Arg   string // header name or JSONPath
	Path  *jsonPath
	Count bool // the count filter
	Not   bool
	Pred  string
	Want  *Value // nil for predicates without a value
}

// Value is an expected value as written.
type Value struct {
	Kind byte // 's' string, 'n' number, 'b' bool, '0' null, 'r' regex, 't' {{template}}
	Text string
}

// none is the result of a query that selects nothing.
type none struct{}

var (
	unaryPreds = map[string]bool{
		"exists": true, "isEmpty": true, "isBoolean": true, "isFloat": true, "isInteger": true,
		"isNumber": true, "isString": true, "isList": true, "isObject": true, "isIsoDate": true,
		"isUuid": true, "isIpv4": true, "isIpv6": true,
	}
	binaryPreds = map[string]bool{
		"==": true, "!=": true, ">": true, ">=": true, "<": true, "<=": true,
		"startsWith": true, "endsWith": true, "contains": true, "includes": true, "matches": true,
	}
	otherQueries = []string{"version", "cookie", "bytes", "rawbytes", "xpath", "regex", "sha256",
		"md5", "url", "redirects", "ip", "variable", "certificate"}
	numberRe = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
	uuidRe   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// lexer splits an assert line into words, quoted strings and values.
type lexer struct {
	s string
	i int
}

func (l *lexer) space() {
	for l.i < len(l.s) && (l.s[l.i] == ' ' || l.s[l.i] == '\t') {
		l.i++
	}
}

func (l *lexer) end() bool { l.space(); return l.i >= len(l.s) || l.s[l.i] == '#' }

// word reads a name or a comparison operator.
func (l *lexer) word() string {
	l.space()
	start := l.i
	if l.i < len(l.s) && strings.IndexByte("=!<>", l.s[l.i]) >= 0 {
		for l.i < len(l.s) && strings.IndexByte("=!<>", l.s[l.i]) >= 0 {
			l.i++
		}
		return l.s[start:l.i]
	}
	for l.i < len(l.s) && (l.s[l.i] >= 'a' && l.s[l.i] <= 'z' || l.s[l.i] >= 'A' && l.s[l.i] <= 'Z' || l.s[l.i] >= '0' && l.s[l.i] <= '9') {
		l.i++
	}
	return l.s[start:l.i]
}

// delimited reads text between q quotes, with backslash escapes.
func (l *lexer) delimited(q byte) (string, error) {
	l.space()
	if l.i >= len(l.s) || l.s[l.i] != q {
		return "", fmt.Errorf("want %c", q)
	}
	var b strings.Builder
	for l.i++; l.i < len(l.s); l.i++ {
		c := l.s[l.i]
		switch {
		case c == q:
			l.i++
			return b.String(), nil
		case c == '\\' && l.i+1 < len(l.s):
			l.i++
			switch e := l.s[l.i]; {
			case q == '/' && e != '/':
				b.WriteByte('\\') // regexes keep their escapes
				b.WriteByte(e)
			case e == 'n':
				b.WriteByte('\n')
			case e == 't':
				b.WriteByte('\t')
			case e == 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(e)
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", fmt.Errorf("unterminated %c", q)
}

func parseAssert(text string, line int) (Assert, error) {
	a := Assert{Line: line, Text: text}
	fail := func(format string, args ...any) (Assert, error) {
		return a, parseErr(line, format, args...)
	}
	l := &lexer{s: text}
	a.Query = l.word()
	switch a.Query {
	case "status", "body", "duration":
	case "header", "jsonpath":
		arg, err := l.delimited('"')
		if err != nil {
			return fail("%s needs a quoted argument: %v", a.Query, err)
		}
		a.Arg = arg
		if a.Query == "jsonpath" {
			p, err := parseJSONPath(arg)
			if err != nil {
				return fail("%v", err)
			}
			a.Path = p
		}
	case "":
		return fail("want an assert like jsonpath \"$.id\" == 1, got %q", text)
	default:
		for _, q := range otherQueries {
			if q == a.Query {
				return fail("query %s is not supported", q)
			}
		}
		return fail("unknown query %q", a.Query)
	}

	w := l.word()
	if w == "count" {
		a.Count = true
		w = l.word()
	}
	if w == "not" {
		a.Not = true
		w = l.word()
	}
	switch {
	case unaryPreds[w]:
		a.Pred = w
	case binaryPreds[w]:
		a.Pred = w
		v, err := l.value()
		if err != nil {
			return fail("%s needs a value: %v", w, err)
		}
		if v.Kind == 'r' && w != "matches" {
			return fail("a /regex/ only works with matches")
		}
		if v.Kind == 'r' {
			if _, err := regexp.Compile(v.Text); err != nil {
				return fail("bad regex: %v", err)
			}
		}
		a.Want = &v
	case w == "":
		return fail("want a predicate such as == or exists after %s", a.Query)
	default:
		return fail("%s is not supported (want a predicate such as == or exists; count is the only filter)", w)
	}
	if !l.end() {
		return fail("unexpected %q", l.s[l.i:])
	}
	return a, nil
}

// value reads an expected value.
func (l *lexer) value() (Value, error) {
	l.space()
	rest := l.s[l.i:]
	switch {
	case strings.HasPrefix(rest, `"`):
		s, err := l.delimited('"')
		return Value{Kind: 's', Text: s}, err
	case strings.HasPrefix(rest, "/"):
		s, err := l.delimited('/')
		return Value{Kind: 'r', Text: s}, err
	case strings.HasPrefix(rest, "{{"):
		end := strings.Index(rest, "}}")
		if end < 0 {
			return Value{}, fmt.Errorf("unterminated {{")
		}
		l.i += end + 2
		return Value{Kind: 't', Text: rest[:end+2]}, nil
	}
	tok := rest
	if n := strings.IndexAny(rest, " \t#"); n >= 0 {
		tok = rest[:n]
	}
	l.i += len(tok)
	switch {
	case tok == "true" || tok == "false":
		return Value{Kind: 'b', Text: tok}, nil
	case tok == "null":
		return Value{Kind: '0'}, nil
	case numberRe.MatchString(tok):
		return Value{Kind: 'n', Text: tok}, nil
	case tok == "":
		return Value{}, fmt.Errorf("missing")
	}
	return Value{}, fmt.Errorf("%q is not a string, number, boolean, null, /regex/ or {{variable}}", tok)
}

// want returns the expected value with templates filled: a string,
// json.Number, bool, nil or *regexp.Regexp.
func (a *Assert) want(look Lookup) (any, error) {
	v := a.Want
	switch v.Kind {
	case 's':
		s, err := expand(v.Text, a.Line, look)
		if err != nil {
			return nil, err
		}
		if a.Pred == "matches" {
			re, err := regexp.Compile(s)
			if err != nil {
				return nil, &Error{Line: a.Line, Msg: "bad regex: " + err.Error(), Code: ExitRuntime}
			}
			return re, nil
		}
		return s, nil
	case 'r':
		return regexp.MustCompile(v.Text), nil
	case 'n':
		return json.Number(v.Text), nil
	case 'b':
		return v.Text == "true", nil
	case '0':
		return nil, nil
	}
	// An unquoted template takes the type its value looks like.
	s, err := expand(v.Text, a.Line, look)
	if err != nil {
		return nil, err
	}
	switch {
	case numberRe.MatchString(s):
		return json.Number(s), nil
	case s == "true" || s == "false":
		return s == "true", nil
	case s == "null":
		return nil, nil
	}
	return s, nil
}

// check reports whether got satisfies the assert. got is none{} when the
// query selected nothing: then, as in hurl, every predicate fails and
// every negated one passes.
func (a *Assert) check(got, want any) bool {
	if _, ok := got.(none); ok {
		return a.Not
	}
	return (a.Pred == "exists" || test(a.Pred, got, want)) != a.Not
}

func test(pred string, got, want any) bool {
	switch pred {
	case "==":
		return equal(got, want)
	case "!=":
		return !equal(got, want)
	case ">", ">=", "<", "<=":
		c, ok := compare(got, want)
		return ok && map[string]bool{">": c > 0, ">=": c >= 0, "<": c < 0, "<=": c <= 0}[pred]
	case "contains", "includes": // hurl deprecates includes for contains
		if l, ok := got.([]any); ok {
			return slices.ContainsFunc(l, func(x any) bool { return equal(x, want) })
		}
		g, ok1 := got.(string)
		w, ok2 := want.(string)
		return pred == "contains" && ok1 && ok2 && strings.Contains(g, w)
	case "startsWith", "endsWith":
		g, ok1 := got.(string)
		w, ok2 := want.(string)
		if !ok1 || !ok2 {
			return false
		}
		return map[string]func(string, string) bool{
			"startsWith": strings.HasPrefix, "endsWith": strings.HasSuffix,
		}[pred](g, w)
	case "matches":
		g, ok := got.(string)
		return ok && want.(*regexp.Regexp).MatchString(g)
	case "isEmpty":
		switch g := got.(type) {
		case string:
			return g == ""
		case []any:
			return len(g) == 0
		case map[string]any:
			return len(g) == 0
		}
		return false
	case "isBoolean":
		_, ok := got.(bool)
		return ok
	case "isNumber":
		_, ok := got.(json.Number)
		return ok
	case "isInteger", "isFloat":
		n, ok := got.(json.Number)
		float := strings.ContainsAny(string(n), ".eE")
		return ok && float == (pred == "isFloat")
	case "isString":
		_, ok := got.(string)
		return ok
	case "isList":
		_, ok := got.([]any)
		return ok
	case "isObject":
		_, ok := got.(map[string]any)
		return ok
	case "isIsoDate":
		g, ok := got.(string)
		if !ok {
			return false
		}
		_, err1 := time.Parse(time.RFC3339Nano, g)
		_, err2 := time.Parse(time.DateOnly, g)
		return err1 == nil || err2 == nil
	case "isUuid":
		g, ok := got.(string)
		return ok && uuidRe.MatchString(g)
	case "isIpv4", "isIpv6":
		g, ok := got.(string)
		if !ok {
			return false
		}
		ip, err := netip.ParseAddr(g)
		return err == nil && ip.Is4() == (pred == "isIpv4")
	}
	return false
}

// equal compares scalars: numbers by value, strings, booleans and null.
func equal(got, want any) bool {
	if c, ok := compare(got, want); ok {
		return c == 0
	}
	switch w := want.(type) {
	case bool:
		g, ok := got.(bool)
		return ok && g == w
	case nil:
		return got == nil
	}
	return false
}

// compare orders two numbers or two strings.
func compare(got, want any) (int, bool) {
	switch w := want.(type) {
	case json.Number:
		g, ok := got.(json.Number)
		if !ok {
			return 0, false
		}
		x, ok1 := new(big.Rat).SetString(string(g))
		y, ok2 := new(big.Rat).SetString(string(w))
		if !ok1 || !ok2 {
			return 0, false
		}
		return x.Cmp(y), true
	case string:
		g, ok := got.(string)
		return strings.Compare(g, w), ok
	}
	return 0, false
}

// show renders an actual value for a failure message.
func show(v any) string {
	var s string
	switch x := v.(type) {
	case none:
		return "none"
	case string:
		s = strconv.Quote(x)
	case json.Number:
		return string(x)
	default:
		b, _ := json.Marshal(x)
		s = string(b)
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
