package hurl

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	var got struct{ method, path, query, auth, ctype, body string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.query = r.Method, r.URL.Path, r.URL.RawQuery
		got.auth, got.ctype, got.body = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(b)
		w.Header().Set("Location", "/orders/1")
		w.WriteHeader(201)
		io.WriteString(w, `{"id":1}`)
	}))
	defer srv.Close()

	vars := map[string]string{"API_URL": srv.URL, "API_TOKEN": "t0ken", "sku": "w"}
	var out strings.Builder
	r := &Runner{Client: NewClient(0), Out: &out, Lookup: func(k string) (string, bool) { v, ok := vars[k]; return v, ok }}
	es, err := Parse(orders)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), es[0]); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/orders" || got.query != "dry=false" || got.auth != "Bearer t0ken" ||
		got.ctype != "application/json" || !strings.Contains(got.body, `"quantity": 1`) {
		t.Errorf("server saw %+v", got)
	}
	want := "POST " + srv.URL + "/orders?dry=false\nHTTP/1.1 201 Created  "
	if !strings.HasPrefix(out.String(), want) || !strings.HasSuffix(out.String(), "{\n  \"id\": 1\n}\n") {
		t.Errorf("output:\n%s", out.String())
	}

	// HTTP/2 * fails on an HTTP/1.1 server, at the HTTP line.
	err = r.Run(context.Background(), es[1])
	var e *Error
	if !errors.As(err, &e) || e.Code != ExitAssert || e.Line != 15 || e.Msg != "HTTP/2: got HTTP/1.1" {
		t.Errorf("version check = %v", err)
	}
}

func TestRunAsserts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-A", "1")
		w.WriteHeader(404)
	}))
	defer srv.Close()
	r := &Runner{Client: NewClient(0), Out: io.Discard, Lookup: func(string) (string, bool) { return "", false }}
	for _, tc := range []struct{ src, want string }{
		{"GET " + srv.URL + "\nHTTP 200", "2: HTTP 200: got 404"},
		{"GET " + srv.URL + "\nHTTP 404\nX-A: 2", `3: header X-A: want "2", got "1"`},
		{"GET " + srv.URL + "\nHTTP 404\nX-B: 2", `3: header X-B: want "2", got none`},
		{"GET {{nope}}/x", "1: undefined variable nope (set GROWL_VARIABLE_nope, HURL_VARIABLE_nope or nope)"},
	} {
		es, err := Parse(tc.src)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Run(context.Background(), es[0]); err == nil || err.Error() != tc.want {
			t.Errorf("%q: got %v, want %s", tc.src, err, tc.want)
		}
	}
	// No HTTP line: any status passes.
	es, _ := Parse("GET " + srv.URL)
	if err := r.Run(context.Background(), es[0]); err != nil {
		t.Errorf("no HTTP line: %v", err)
	}
}

func TestNoRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	es, _ := Parse("GET " + srv.URL + "\nHTTP 302\nLocation: /elsewhere")
	r := &Runner{Client: NewClient(0), Out: io.Discard, Lookup: func(string) (string, bool) { return "", false }}
	if err := r.Run(context.Background(), es[0]); err != nil {
		t.Error(err)
	}
}

func TestRequestLikeHurl(t *testing.T) {
	var query, ctype, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		query, ctype, body = r.URL.RawQuery, r.Header.Get("Content-Type"), string(b)
	}))
	defer srv.Close()
	r := &Runner{Client: NewClient(0), Out: io.Discard, Lookup: func(string) (string, bool) { return "", false }}
	send := func(src string) {
		t.Helper()
		es, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Run(context.Background(), es[0]); err != nil {
			t.Fatal(err)
		}
	}

	// [Query] appends to the URL's query in order, spaces as %20.
	send("GET " + srv.URL + "/?z=1\n[Query]\nb: 2\nq: a b&c+d\n")
	if query != "z=1&b=2&q=a%20b%26c%2Bd" {
		t.Errorf("query = %q", query)
	}
	// A JSON body ends at its closing bracket.
	send("POST " + srv.URL + "\n{\"a\": [1,\n 2]}  \n")
	if body != "{\"a\": [1,\n 2]}" || ctype != "application/json" {
		t.Errorf("JSON body = %q, %q", body, ctype)
	}
	// ```json is sent as JSON; ``` as is, without a type.
	send("POST " + srv.URL + "\n```json\n{\"a\": 1}\n```\n")
	if body != "{\"a\": 1}\n" || ctype != "application/json" {
		t.Errorf("```json body = %q, %q", body, ctype)
	}
	send("POST " + srv.URL + "\n```\nhi\n```\n")
	if body != "hi\n" || ctype != "" {
		t.Errorf("``` body = %q, %q", body, ctype)
	}
}
