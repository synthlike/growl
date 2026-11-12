package hurl

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// jsonPath is a small JSONPath subset: $, .name, ['name'], [n] (negative
// counts from the end), .* and [*], and ..name or ..* for descendants.
// Filters, slices and unions are rejected when parsed.
type jsonPath struct {
	steps    []pathStep
	definite bool // no wildcard or descent: the result is one value or none
}

type pathStep struct {
	kind  byte // 'f' field, 'i' index, '*' wildcard, 'd' descendants
	name  string
	index int
}

func parseJSONPath(s string) (*jsonPath, error) {
	if !strings.HasPrefix(s, "$") {
		return nil, fmt.Errorf("JSONPath %q must start with $", s)
	}
	p := &jsonPath{definite: true}
	for i := 1; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], ".."):
			i += 2
			name, n := pathName(s[i:])
			if n == 0 {
				return nil, fmt.Errorf("JSONPath %q: want a name or * after ..", s)
			}
			p.steps = append(p.steps, pathStep{kind: 'd', name: name})
			p.definite = false
			i += n
		case s[i] == '.':
			i++
			name, n := pathName(s[i:])
			if n == 0 {
				return nil, fmt.Errorf("JSONPath %q: want a name or * after .", s)
			}
			if name == "*" {
				p.steps = append(p.steps, pathStep{kind: '*'})
				p.definite = false
			} else {
				p.steps = append(p.steps, pathStep{kind: 'f', name: name})
			}
			i += n
		case s[i] == '[':
			st, n, err := bracket(s[i:])
			if err != nil {
				return nil, fmt.Errorf("JSONPath %q: %v", s, err)
			}
			if st.kind == '*' {
				p.definite = false
			}
			p.steps = append(p.steps, st)
			i += n
		default:
			return nil, fmt.Errorf("JSONPath %q: unexpected %q", s, s[i:])
		}
	}
	return p, nil
}

// pathName reads a member name, or *, and its length.
func pathName(s string) (string, int) {
	if strings.HasPrefix(s, "*") {
		return "*", 1
	}
	n := 0
	for n < len(s) {
		c := s[n]
		if c != '_' && c != '-' && c != '$' && !('a' <= c && c <= 'z') && !('A' <= c && c <= 'Z') && !('0' <= c && c <= '9') && c < 0x80 {
			break
		}
		n++
	}
	return s[:n], n
}

// bracket reads [n], [*] or ['name'] at the start of s.
func bracket(s string) (pathStep, int, error) {
	if len(s) > 1 && (s[1] == '\'' || s[1] == '"') {
		q := s[1]
		var name strings.Builder
		for i := 2; i < len(s); i++ {
			switch {
			case s[i] == '\\' && i+1 < len(s):
				i++
				name.WriteByte(s[i])
			case s[i] == q:
				if i+1 >= len(s) || s[i+1] != ']' {
					return pathStep{}, 0, fmt.Errorf("unions like ['a','b'] are not supported")
				}
				return pathStep{kind: 'f', name: name.String()}, i + 2, nil
			default:
				name.WriteByte(s[i])
			}
		}
		return pathStep{}, 0, fmt.Errorf("unterminated %c", q)
	}
	end := strings.IndexByte(s, ']')
	if end < 0 {
		return pathStep{}, 0, fmt.Errorf("unterminated [")
	}
	inner := strings.TrimSpace(s[1:end])
	switch {
	case inner == "*":
		return pathStep{kind: '*'}, end + 1, nil
	case strings.HasPrefix(inner, "?"):
		return pathStep{}, 0, fmt.Errorf("filters like [?(...)] are not supported")
	case strings.Contains(inner, ":"):
		return pathStep{}, 0, fmt.Errorf("slices like [0:2] are not supported")
	case strings.Contains(inner, ","):
		return pathStep{}, 0, fmt.Errorf("unions like [0,1] are not supported")
	}
	n, err := strconv.Atoi(inner)
	if err != nil {
		return pathStep{}, 0, fmt.Errorf("want an index, * or a quoted name in [%s]", inner)
	}
	return pathStep{kind: 'i', index: n}, end + 1, nil
}

// eval returns the nodes the path selects in a decoded JSON document.
// Object members are visited in key order: decoding loses document order.
func (p *jsonPath) eval(root any) []any {
	nodes := []any{root}
	for _, st := range p.steps {
		var next []any
		for _, n := range nodes {
			switch st.kind {
			case 'f':
				if m, ok := n.(map[string]any); ok {
					if v, ok := m[st.name]; ok {
						next = append(next, v)
					}
				}
			case 'i':
				if a, ok := n.([]any); ok {
					i := st.index
					if i < 0 {
						i += len(a)
					}
					if 0 <= i && i < len(a) {
						next = append(next, a[i])
					}
				}
			case '*':
				next = append(next, children(n)...)
			case 'd':
				next = descend(n, st.name, next)
			}
		}
		nodes = next
	}
	return nodes
}

func children(n any) []any {
	switch v := n.(type) {
	case map[string]any:
		var out []any
		for _, k := range slices.Sorted(maps.Keys(v)) {
			out = append(out, v[k])
		}
		return out
	case []any:
		return v
	}
	return nil
}

// descend appends the members called name (or all, for *) of n and of
// everything under it.
func descend(n any, name string, out []any) []any {
	if m, ok := n.(map[string]any); ok && name != "*" {
		if v, ok := m[name]; ok {
			out = append(out, v)
		}
	}
	for _, c := range children(n) {
		if name == "*" {
			out = append(out, c)
		}
		out = descend(c, name, out)
	}
	return out
}
