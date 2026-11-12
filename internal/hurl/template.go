package hurl

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var templateRe = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*\}\}`)

// Lookup finds a variable's value in the environment.
type Lookup func(name string) (string, bool)

// scope is where templates find values.
type scope interface {
	// value returns a captured value (string, json.Number, bool, nil, or
	// a list or object) or an envString.
	value(name string) (any, bool)
	// hint says how to define an undefined variable.
	hint(name string) string
}

// envString is a variable's value from the environment. Compared unquoted,
// it takes the type its text looks like.
type envString string

// expand fills {{name}} templates in s, written at line. newUuid and newDate
// are built in, as in hurl. A missing variable is an error: it never becomes
// an empty string. So is a list or object, which hurl can't render either.
func expand(s string, line int, sc scope) (string, error) {
	var missing []string
	var bad error
	out := templateRe.ReplaceAllStringFunc(s, func(m string) string {
		name := templateRe.FindStringSubmatch(m)[1]
		switch name {
		case "newUuid":
			return uuid()
		case "newDate":
			return time.Now().UTC().Format(time.RFC3339Nano)
		}
		v, ok := sc.value(name)
		if !ok {
			missing = append(missing, name)
			return m
		}
		text, err := render(v)
		if err != nil && bad == nil {
			bad = &Error{Line: line, Code: ExitRuntime, Msg: fmt.Sprintf("{{%s}} is %s, which can't be rendered", name, show(v))}
		}
		return text
	})
	if len(missing) > 0 {
		return "", &Error{Line: line, Code: ExitRuntime,
			Msg: fmt.Sprintf("undefined variable %s (%s)", strings.Join(missing, ", "), sc.hint(missing[0]))}
	}
	if bad != nil {
		return "", bad
	}
	return out, nil
}

// render writes a value into a template as hurl does: integers exactly,
// floats in their shortest form with at least one decimal, null as null.
func render(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case envString:
		return string(x), nil
	case bool:
		return strconv.FormatBool(x), nil
	case nil:
		return "null", nil
	case json.Number:
		s := string(x)
		if !strings.ContainsAny(s, ".eE") {
			if i, ok := new(big.Int).SetString(s, 10); ok {
				return i.String(), nil
			}
			return s, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return s, nil
		}
		out := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(out, ".") {
			out += ".0"
		}
		return out, nil
	}
	return "", fmt.Errorf("unrenderable")
}

// uuid returns a random version 4 UUID.
func uuid() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
