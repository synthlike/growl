package hurl

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var templateRe = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*\}\}`)

// Lookup finds a variable's value.
type Lookup func(name string) (string, bool)

// expand fills {{name}} templates in s, written at line. newUuid and newDate
// are built in, as in hurl. A missing variable is an error: it never becomes
// an empty string.
func expand(s string, line int, look Lookup) (string, error) {
	var missing []string
	out := templateRe.ReplaceAllStringFunc(s, func(m string) string {
		name := templateRe.FindStringSubmatch(m)[1]
		switch name {
		case "newUuid":
			return uuid()
		case "newDate":
			return time.Now().UTC().Format(time.RFC3339Nano)
		}
		if v, ok := look(name); ok {
			return v
		}
		missing = append(missing, name)
		return m
	})
	if len(missing) > 0 {
		return "", &Error{Line: line, Code: ExitRuntime,
			Msg: fmt.Sprintf("undefined variable %s (set HURL_VARIABLE_%s or %s)", strings.Join(missing, ", "), missing[0], missing[0])}
	}
	return out, nil
}

// uuid returns a random version 4 UUID.
func uuid() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
