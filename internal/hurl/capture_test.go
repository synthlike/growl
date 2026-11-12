package hurl

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseCapture(t *testing.T) {
	c, err := parseCapture(`order-id: jsonpath "$.items" count redact # note`, 9)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "order-id" || c.Kind != "jsonpath" || c.Arg != "$.items" || !c.Count || !c.Redact {
		t.Errorf("got %+v", c)
	}
	for text, want := range map[string]string{
		`id jsonpath "$.id"`:       `want a capture like id: jsonpath "$.id"`,
		`id:`:                      `want a query like jsonpath "$.id", got ""`,
		`id: xpath "//a"`:          "query xpath is not supported",
		`id: jsonpath "$.a" nth 0`: "nth is not supported (count is the only filter)",
		`id: status redact extra`:  `unexpected "extra"`,
		`id: jsonpath "$.a[0:1]"`:  `JSONPath "$.a[0:1]": slices like [0:2] are not supported`,
		`1id: status`:              `want a capture like id: jsonpath "$.id"`,
	} {
		_, err := parseCapture(text, 3)
		if err == nil || !strings.Contains(err.Error(), "3: "+want) {
			t.Errorf("%s: got %v, want %q", text, err, want)
		}
	}
}

func TestRender(t *testing.T) {
	for in, want := range map[string]string{
		"7": "7", "-0": "0", "12345678901234567890": "12345678901234567890",
		"9.50": "9.5", "1.0": "1.0", "1e3": "1000.0", "1E2": "100.0", "1e-7": "0.0000001",
		"0.30000000000000004": "0.30000000000000004", "2.5e-3": "0.0025",
	} {
		if got, err := render(json.Number(in)); err != nil || got != want {
			t.Errorf("render(%s) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, v := range []any{"s", envString("e"), true, nil} {
		if _, err := render(v); err != nil {
			t.Errorf("render(%v): %v", v, err)
		}
	}
	for _, v := range []any{[]any{1}, map[string]any{}} {
		if _, err := render(v); err == nil {
			t.Errorf("render(%v) should fail", v)
		}
	}
}

func TestCaptures(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, r.URL.RequestURI()+" "+string(b))
		w.Header().Set("X-Token", "tok-123456")
		io.WriteString(w, `{"id": 7, "s": "7", "price": 9.50, "tags": ["a", "b"], "token": "tok-123456"}`)
	}))
	defer srv.Close()
	env := map[string]string{"base": srv.URL, "s": "from-env"}
	var secrets []string
	r := &Runner{Client: NewClient(0), Out: io.Discard,
		Lookup:   func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		OnSecret: func(s string) { secrets = append(secrets, s) },
	}
	src := `GET {{base}}/first
HTTP 200
[Captures]
id: jsonpath "$.id"
s: jsonpath "$.s"
price: jsonpath "$.price"
n: jsonpath "$.tags" count
tok: header "X-Token" redact
[Asserts]
jsonpath "$.id" == {{id}}
jsonpath "$.s" == {{s}}
jsonpath "$.s" != {{id}}

POST {{base}}/orders/{{id}}?p={{price}}&n={{n}}
{"s": "{{s}}", "tok": "{{tok}}"}
`
	es, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if err := r.Run(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	// The capture s overrides the environment's s, and keeps its type: the
	// string "7" isn't the number 7.
	want := `/orders/7?p=9.5&n=2 {"s": "7", "tok": "tok-123456"}`
	if len(seen) != 2 || seen[1] != want {
		t.Errorf("second request = %q, want %q", seen, want)
	}
	if len(secrets) != 1 || secrets[0] != "tok-123456" {
		t.Errorf("secrets = %q", secrets)
	}
}

func TestCaptureErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Thing", "a")
		w.Header().Add("Set-Thing", "b")
		io.WriteString(w, `{"tags": ["a"], "name": "x"}`)
	}))
	defer srv.Close()
	for src, want := range map[string]string{
		"GET {{base}}\nHTTP 200\n[Captures]\nx: jsonpath \"$.nope\"":                            `4: x: jsonpath "$.nope": selects nothing`,
		"GET {{base}}\nHTTP 200\n[Captures]\nx: jsonpath \"$.name\" count":                      `4: x: jsonpath "$.name" count: count needs a list, got "x"`,
		"GET {{base}}\nHTTP 200\n[Captures]\nt: jsonpath \"$.tags\"\nGET {{base}}/{{t}}":        `5: {{t}} is ["a"], which can't be rendered`,
		"GET {{base}}\nHTTP 200\n[Captures]\nh: header \"Set-Thing\"\n[Asserts]\nbody == {{h}}": `6: {{h}} is ["a","b"], which can't be compared`,
		// The HTTP line is checked before captures.
		"GET {{base}}\nHTTP 201\n[Captures]\nx: jsonpath \"$.nope\"": "2: HTTP 201: got 200",
	} {
		r := &Runner{Client: NewClient(0), Out: io.Discard, Lookup: func(k string) (string, bool) { return srv.URL, k == "base" }}
		es, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		var runErr error
		for _, e := range es {
			if runErr = r.Run(context.Background(), e); runErr != nil {
				break
			}
		}
		if runErr == nil || runErr.Error() != want {
			t.Errorf("%q: got %v, want %s", src, runErr, want)
		}
	}
}

func TestUndefinedHint(t *testing.T) {
	r := &Runner{Lookup: func(string) (string, bool) { return "", false }, CapturedAt: map[string]int{"id": 2}}
	if _, err := expand("{{id}}", 9, r); err == nil || err.Error() != "9: undefined variable id (captured by the request at line 2; run up to here with -to)" {
		t.Errorf("got %v", err)
	}
	if _, err := expand("{{other}}", 9, r); err == nil || !strings.Contains(err.Error(), "set GROWL_VARIABLE_other, HURL_VARIABLE_other or other") {
		t.Errorf("got %v", err)
	}
}
