package hurl

import (
	"errors"
	"strings"
	"testing"
)

const orders = `# Create an order, then read it back
POST {{API_URL}}/orders
Authorization: Bearer {{API_TOKEN}}
[Query]
dry: false
{
  "sku": "demo-widget",
  "note": "a } in a string",
  "quantity": 1
}
HTTP 201
Location: /orders/1

GET {{API_URL}}/orders/1
HTTP/2 *

DELETE {{API_URL}}/orders/1
` + "```" + `
plain
text
` + "```" + `
`

func TestParse(t *testing.T) {
	es, err := Parse(orders)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 {
		t.Fatalf("got %d entries, want 3", len(es))
	}
	e := es[0]
	if e.Line != 2 || e.Method != "POST" || e.URL != "{{API_URL}}/orders" {
		t.Errorf("request = %d %s %s", e.Line, e.Method, e.URL)
	}
	if len(e.Headers) != 1 || e.Headers[0] != (Pair{3, "Authorization", "Bearer {{API_TOKEN}}"}) {
		t.Errorf("headers = %v", e.Headers)
	}
	if len(e.Query) != 1 || e.Query[0] != (Pair{5, "dry", "false"}) {
		t.Errorf("query = %v", e.Query)
	}
	if e.Body == nil || !e.Body.JSON || e.Body.Line != 6 || !strings.HasSuffix(e.Body.Text, "\n}") {
		t.Errorf("body = %+v", e.Body)
	}
	r := e.Response
	if r == nil || r.Line != 11 || r.Status != 201 || r.Version != "" || len(r.Headers) != 1 || r.Headers[0].Line != 12 {
		t.Errorf("response = %+v", r)
	}
	if r := es[1].Response; r == nil || r.Version != "2" || r.Status != 0 {
		t.Errorf("wildcard response = %+v", r)
	}
	if b := es[2].Body; b == nil || b.JSON || b.Text != "plain\ntext\n" {
		t.Errorf("text body = %+v", b)
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"hello", `1: want a request line like GET https://example.com, got "hello"`},
		{"GET http://x\n[Options]\nverbose: true", "2: [Options] is not supported"},
		{"GET http://x\nHTTP 200\n[Captures]\nid: jsonpath \"$.id\"", "3: [Captures] is not supported yet"},
		{"GET http://x\nHTTP 200\n[Query]", "3: [Query] belongs to the request, before the HTTP line"},
		{"GET http://x\n[Captures]", "2: [Captures] goes after the HTTP line"},
		{"POST http://x\n{\n\"a\": 1", "2: unterminated JSON body"},
		{"POST http://x\n<a/>", "2: XML bodies are not supported"},
		{"POST http://x\nfile,data.bin;", "2: file bodies are not supported"},
		{"GET http://x\nHTTP 200\n{}", "3: response body asserts are not supported yet"},
		{"GET http://x\nHTTP 200\nnot a header", `3: unexpected "not a header" (want a header, section, body, HTTP line or the next request)`},
	} {
		_, err := Parse(tc.src)
		var e *Error
		if !errors.As(err, &e) || e.Error() != tc.want || e.Code != ExitParse {
			t.Errorf("Parse(%q) = %v, want %s", tc.src, err, tc.want)
		}
	}
}

func TestParseJSONArrayIsNotASection(t *testing.T) {
	es, err := Parse("POST http://x\n[true]\n")
	if err != nil || es[0].Body == nil || es[0].Body.Text != "[true]" {
		t.Fatalf("got %+v, %v", es, err)
	}
}

func TestPick(t *testing.T) {
	es, _ := Parse(orders)
	for line, want := range map[int]int{1: 0, 2: 0, 13: 0, 14: 1, 15: 1, 17: 2, 99: 2} {
		if got := Pick(es, line); got != want {
			t.Errorf("Pick(%d) = %d, want %d", line, got, want)
		}
	}
}
