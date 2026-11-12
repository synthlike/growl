package hurl

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAssert(t *testing.T) {
	a, err := parseAssert(`jsonpath "$.items" count not == 3 # comment`, 7)
	if err != nil {
		t.Fatal(err)
	}
	if a.Query != "jsonpath" || a.Arg != "$.items" || !a.Count || !a.Not || a.Pred != "==" || *a.Want != (Value{'n', "3"}) {
		t.Errorf("got %+v", a)
	}
	a, err = parseAssert(`header "X-Re" matches /^a\/b\d+$/`, 1)
	if err != nil || a.Want.Text != `^a/b\d+$` {
		t.Errorf("regex: %+v, %v", a.Want, err)
	}
	a, err = parseAssert(`body contains "say \"hi\"\n"`, 1)
	if err != nil || a.Want.Text != "say \"hi\"\n" {
		t.Errorf("escapes: %+v, %v", a.Want, err)
	}

	for text, want := range map[string]string{
		`xpath "//a" exists`:              "query xpath is not supported",
		`colour == 1`:                     `unknown query "colour"`,
		`jsonpath $.id == 1`:              "jsonpath needs a quoted argument: want \"",
		`jsonpath "$.a[?(@.b)]" exists`:   `JSONPath "$.a[?(@.b)]": filters like [?(...)] are not supported`,
		`status`:                          "want a predicate such as == or exists after status",
		`jsonpath "$.a" nth 0 == 1`:       "nth is not supported (want a predicate such as == or exists; count is the only filter)",
		`status ==`:                       "== needs a value: missing",
		`status == two`:                   `== needs a value: "two" is not a string, number, boolean, null, /regex/ or {{variable}}`,
		`body == /x/`:                     "a /regex/ only works with matches",
		`body matches /(/`:                "bad regex",
		`status == 200 201`:               `unexpected "201"`,
		`jsonpath "$.a" == "unterminated`: "== needs a value: unterminated \"",
	} {
		_, err := parseAssert(text, 4)
		if err == nil || !strings.Contains(err.Error(), "4: "+want) {
			t.Errorf("%s: got %v, want %q", text, err, want)
		}
	}
}

const shop = `{"id": 7, "price": 9.50, "name": "Dune", "tags": ["sf", "classic"],
	"owner": null, "ok": true, "made": "2026-11-12T20:00:00Z", "ip": "10.0.0.1",
	"uuid": "8d86c9b4-e813-48e9-a416-f2a63a516d2b", "empty": [], "meta": {"a": 1}}`

func TestAsserts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Thing", "a=1")
		w.Header().Add("Set-Thing", "b=2")
		io.WriteString(w, shop)
	}))
	defer srv.Close()
	vars := map[string]string{"id": "7", "name": "Dune"}
	r := &Runner{Client: NewClient(0), Out: io.Discard, Lookup: func(k string) (string, bool) { v, ok := vars[k]; return v, ok }}

	pass := []string{
		`status == 200`, `status < 300`, `duration < 10000`,
		`jsonpath "$.id" == 7`, `jsonpath "$.id" == {{id}}`, `jsonpath "$.id" != "7"`,
		`jsonpath "$.price" == 9.5`, `jsonpath "$.price" > 9`, `jsonpath "$.price" isFloat`,
		`jsonpath "$.id" isInteger`, `jsonpath "$.id" isNumber`, `jsonpath "$.name" == "{{name}}"`,
		`jsonpath "$.name" startsWith "Du"`, `jsonpath "$.name" endsWith "ne"`, `jsonpath "$.name" contains "un"`,
		`jsonpath "$.name" matches /^D.n/`, `jsonpath "$.name" matches "^D"`, `jsonpath "$.name" > "Cat"`,
		`jsonpath "$.tags" includes "sf"`, `jsonpath "$.tags" count == 2`, `jsonpath "$.tags[*]" count == 2`,
		`jsonpath "$.meta" count == 1`, `jsonpath "$.tags" isList`, `jsonpath "$.meta" isObject`,
		`jsonpath "$.owner" == null`, `jsonpath "$.owner" exists`, `jsonpath "$.nope" not exists`,
		`jsonpath "$..nope" not exists`, `jsonpath "$.ok" == true`, `jsonpath "$.ok" isBoolean`,
		`jsonpath "$.made" isIsoDate`, `jsonpath "$.ip" isIpv4`, `jsonpath "$.uuid" isUuid`,
		`jsonpath "$.empty" isEmpty`, `jsonpath "$.name" not isEmpty`, `jsonpath "$.name" isString`,
		`header "Set-Thing" == "b=2"`, `header "Set-Thing" not == "c=3"`, `header "Nope" not exists`,
		`body contains "classic"`,
	}
	fail := map[string]string{
		`jsonpath "$.id" == 8`:            "got 7",
		`jsonpath "$.id" == "7"`:          "got 7",
		`jsonpath "$.nope" == 1`:          "got none",
		`jsonpath "$.nope" not == 1`:      "got none",
		`jsonpath "$.tags" count == 3`:    "got 2",
		`jsonpath "$.name" count == 1`:    `count needs a list or object, got "Dune"`,
		`header "Set-Thing" == "c=3"`:     `got "a=1", "b=2"`,
		`header "Set-Thing" not == "a=1"`: `got "a=1", "b=2"`,
		`jsonpath "$.ip" isIpv6`:          `got "10.0.0.1"`,
		`jsonpath "$.tags" == "sf"`:       `got ["sf","classic"]`,
	}
	run := func(asserts ...string) error {
		src := "GET " + srv.URL + "\nHTTP 200\n[Asserts]\n" + strings.Join(asserts, "\n") + "\n"
		es, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		return r.Run(context.Background(), es[0])
	}
	for _, a := range pass {
		if err := run(a); err != nil {
			t.Errorf("%s: %v", a, err)
		}
	}
	for a, want := range fail {
		if err := run(a); err == nil || err.Error() != "4: "+a+": "+want {
			t.Errorf("%s: got %v, want 4: …: %s", a, err, want)
		}
	}

	// Every failure is reported, at its own line.
	err := run(`status == 201`, `jsonpath "$.id" == 7`, `jsonpath "$.name" == "Emma"`)
	if err == nil || err.Error() != "4: status == 201: got 200\n6: jsonpath \"$.name\" == \"Emma\": got \"Dune\"" {
		t.Errorf("joined: %v", err)
	}
}

func TestAssertsOnNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>")
	}))
	defer srv.Close()
	es, _ := Parse("GET " + srv.URL + "\nHTTP 200\n[Asserts]\njsonpath \"$.a\" exists\nbody == \"<html>\"")
	r := &Runner{Client: NewClient(0), Out: io.Discard, Lookup: func(string) (string, bool) { return "", false }}
	err := r.Run(context.Background(), es[0])
	if err == nil || !strings.HasPrefix(err.Error(), `4: jsonpath "$.a" exists: the body is not JSON`) {
		t.Errorf("got %v", err)
	}
}
