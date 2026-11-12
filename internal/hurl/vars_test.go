package hurl

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestParseVariable(t *testing.T) {
	for in, want := range map[string]struct {
		name  string
		value any
	}{
		"id=7":         {"id", envString("7")},
		`id="7"`:       {"id", "7"},
		`x=""`:         {"x", ""},
		"x=":           {"x", envString("")},
		"x=a=b":        {"x", envString("a=b")},
		"x=a  ":        {"x", envString("a  ")}, // -variable values aren't trimmed
		"x='single'":   {"x", envString("'single'")},
		"x =a":         {"x ", envString("a")},
		`x="a # b"`:    {"x", "a # b"},
		"x=a # not it": {"x", envString("a # not it")},
	} {
		name, v, err := ParseVariable(in)
		if err != nil || name != want.name || v != want.value {
			t.Errorf("ParseVariable(%q) = %q, %#v, %v; want %q, %#v", in, name, v, err, want.name, want.value)
		}
	}
	for in, want := range map[string]string{
		"novalue": "missing value for variable novalue",
		`x="a"b`:  "the value of x should end with a double quote",
		`x="open`: "the value of x should end with a double quote",
		`x="`:     "the value of x should end with a double quote",
	} {
		if _, _, err := ParseVariable(in); err == nil || err.Error() != want {
			t.Errorf("ParseVariable(%q): got %v, want %s", in, err, want)
		}
	}
}

func TestParseVariables(t *testing.T) {
	vars, err := ParseVariables("# comment\n\n  host=example.com  \r\nid=1\nid=2\nname=\"Dune\"\n")
	want := map[string]any{"host": envString("example.com"), "id": envString("2"), "name": "Dune"}
	if err != nil || !reflect.DeepEqual(vars, want) {
		t.Errorf("got %#v, %v", vars, err)
	}
	if _, err := ParseVariables("a=1\nnovalue\n"); err == nil || err.Error() != "line 2: missing value for variable novalue" {
		t.Errorf("got %v", err)
	}
}

func TestVariablePrecedence(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		io.WriteString(w, `{"v": "capture"}`)
	}))
	defer srv.Close()
	env := map[string]string{"base": srv.URL, "a": "env", "b": "env", "c": "env", "q": `"quoted"`}
	r := &Runner{Client: NewClient(0), Out: io.Discard,
		Lookup: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Vars:   map[string]any{"a": envString("vars"), "b": envString("vars")},
	}
	es, err := Parse("GET {{base}}\nHTTP 200\n[Captures]\na: jsonpath \"$.v\"\nGET {{base}}/?a={{a}}&b={{b}}&c={{c}}&q={{q}}\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if err := r.Run(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	// A capture beats Vars, Vars beat the environment, and a quoted
	// environment value loses its quotes.
	if got != "a=capture&b=vars&c=env&q=quoted" {
		t.Errorf("query = %q", got)
	}
}
