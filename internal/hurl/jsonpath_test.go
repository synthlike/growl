package hurl

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONPath(t *testing.T) {
	doc := decode(t, `{"store": {"book": [
		{"title": "Dune", "price": 9, "tags": ["sf"]},
		{"title": "Emma", "price": 4}
	], "my key": {"title": "Odd"}}, "n": null}`)
	for _, tc := range []struct {
		path     string
		want     string // JSON of the selected nodes
		definite bool
	}{
		{"$", "", true},
		{"$.store.book[0].title", `["Dune"]`, true},
		{"$['store']['book'][1]['price']", `[4]`, true},
		{"$.store.book[-1].title", `["Emma"]`, true},
		{"$.store.book[2]", `null`, true},
		{"$.n", `[null]`, true},
		{"$.missing.deeper", `null`, true},
		{"$.store['my key'].title", `["Odd"]`, true},
		{"$.store.book[*].title", `["Dune","Emma"]`, false},
		{"$.store.book.*.price", `[9,4]`, false},
		{"$..title", `["Dune","Emma","Odd"]`, false},
		{"$.store.book[0]..*", `[9,["sf"],"sf","Dune"]`, false},
	} {
		p, err := parseJSONPath(tc.path)
		if err != nil {
			t.Errorf("%s: %v", tc.path, err)
			continue
		}
		if p.definite != tc.definite {
			t.Errorf("%s: definite = %v", tc.path, p.definite)
		}
		if tc.want == "" {
			continue
		}
		got, _ := json.Marshal(p.eval(doc))
		if string(got) != tc.want {
			t.Errorf("%s = %s, want %s", tc.path, got, tc.want)
		}
	}
}

func TestJSONPathErrors(t *testing.T) {
	for path, want := range map[string]string{
		"store.book":      "must start with $",
		"$.a[?(@.x > 1)]": "filters like [?(...)] are not supported",
		"$.a[0:2]":        "slices like [0:2] are not supported",
		"$.a[0,1]":        "unions like [0,1] are not supported",
		"$['a','b']":      "unions like ['a','b'] are not supported",
		"$.a[x]":          "want an index, * or a quoted name in [x]",
		"$.a[0":           "unterminated [",
		"$.":              "want a name or * after .",
		"$a":              `unexpected "a"`,
	} {
		_, err := parseJSONPath(path)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", path, err, want)
		}
	}
}

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
