package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.URL.Path+" "+r.Header.Get("Authorization")+"\n")
	}))
	defer srv.Close()
	env := map[string]string{"HURL_VARIABLE_base": srv.URL, "base": "http://wrong", "API_TOKEN": "s3cret-value"}
	getenv := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	environ := []string{"API_TOKEN=s3cret-value", "PATH=/bin"}
	src := "GET {{base}}/one\nAuthorization: {{API_TOKEN}}\n\nGET {{base}}/two\nHTTP 500\n"
	file := filepath.Join(t.TempDir(), "a.hurl")
	os.WriteFile(file, []byte("GET http://127.0.0.1:1/\n"), 0o644)

	call := func(args ...string) (int, string, string) {
		var out, errOut strings.Builder
		code := run(context.Background(), args, strings.NewReader(src), &out, &errOut, getenv, environ)
		return code, out.String(), errOut.String()
	}

	// -stdin uses the buffer's text; -line picks the first request; the
	// token is hidden in the output.
	code, out, errOut := call("-stdin", "-line", "2", file)
	if code != 0 || !strings.Contains(out, "/one ***\n") || strings.Contains(out, "s3cret") {
		t.Errorf("line 2: code %d\nout:\n%s\nerr: %s", code, out, errOut)
	}
	// The whole file stops at the failing assert, reported as file:line.
	code, _, errOut = call("-stdin", file)
	if code != 4 || errOut != file+":5: HTTP 500: got 200\n" {
		t.Errorf("all: code %d, err %q", code, errOut)
	}
	// Without -stdin the file on disk is read.
	if code, _, errOut = call("-line", "1", file); code != 3 || !strings.HasPrefix(errOut, file+":1: ") {
		t.Errorf("disk: code %d, err %q", code, errOut)
	}
	if code, _, _ = call(); code != 1 {
		t.Errorf("no args: code %d", code)
	}
}

func TestVariablePrecedence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.URL.Path+"\n")
	}))
	defer srv.Close()
	for _, env := range []map[string]string{
		{"GROWL_VARIABLE_p": "growl", "HURL_VARIABLE_p": "hurl", "p": "plain"},
		{"HURL_VARIABLE_p": "hurl", "p": "plain"},
		{"p": "plain"},
	} {
		getenv := func(k string) (string, bool) {
			if k == "base" {
				return srv.URL, true
			}
			v, ok := env[k]
			return v, ok
		}
		var out strings.Builder
		code := run(context.Background(), []string{"-stdin", "x.hurl"}, strings.NewReader("GET {{base}}/{{p}}\n"), &out, io.Discard, getenv, nil)
		want := "/" + map[int]string{3: "growl", 2: "hurl", 1: "plain"}[len(env)] + "\n"
		if code != 0 || !strings.HasSuffix(out.String(), want) {
			t.Errorf("env %v: code %d, output %q, want suffix %q", env, code, out.String(), want)
		}
	}
}

func TestReportsEveryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id": 1}`)
	}))
	defer srv.Close()
	src := "GET " + srv.URL + "\nHTTP 200\n[Asserts]\njsonpath \"$.id\" == 2\njsonpath \"$.name\" exists\n"
	var errOut strings.Builder
	code := run(context.Background(), []string{"-stdin", "a.hurl"}, strings.NewReader(src), io.Discard, &errOut, func(string) (string, bool) { return "", false }, nil)
	want := "a.hurl:4: jsonpath \"$.id\" == 2: got 1\na.hurl:5: jsonpath \"$.name\" exists: got none\n"
	if code != 4 || errOut.String() != want {
		t.Errorf("code %d, stderr:\n%s", code, errOut.String())
	}
}

func TestToAndCaptures(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		io.WriteString(w, `{"id": 42, "token": "tok-abcdef"}`)
	}))
	defer srv.Close()
	src := `POST {{base}}/login
HTTP 200
[Captures]
id: jsonpath "$.id"
token: jsonpath "$.token" redact

GET {{base}}/orders/{{id}}
Authorization: {{token}}

GET {{base}}/last
`
	getenv := func(k string) (string, bool) { return srv.URL, k == "base" }
	call := func(args ...string) (int, string, string) {
		paths = nil
		var out, errOut strings.Builder
		code := run(context.Background(), append(args, "a.hurl"), strings.NewReader(src), &out, &errOut, getenv, nil)
		return code, out.String(), errOut.String()
	}

	// -to on the second request runs the first two; the captured token is
	// hidden everywhere, including in the response that carried it.
	code, out, errOut := call("-stdin", "-to", "8")
	if code != 0 || strings.Join(paths, " ") != "/login /orders/42" {
		t.Errorf("-to: code %d, paths %v, err %s", code, paths, errOut)
	}
	if strings.Contains(out, "tok-abcdef") || !strings.Contains(out, `"token": "***"`) {
		t.Errorf("-to: token not hidden:\n%s", out)
	}
	// -line on the second request alone can't see the capture, and says
	// where it comes from.
	code, _, errOut = call("-stdin", "-line", "7")
	want := "a.hurl:7: undefined variable id (captured by the request at line 1; run up to here with -to)\n"
	if code != 3 || errOut != want || len(paths) != 0 {
		t.Errorf("-line: code %d, paths %v, err %q", code, paths, errOut)
	}
	if code, _, _ = call("-stdin", "-line", "7", "-to", "7"); code != 1 {
		t.Errorf("-line with -to: code %d, want 1", code)
	}
}

func TestVariableFlags(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		io.WriteString(w, `{"id": 7}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(text), 0o644)
		return p
	}
	a := write("a.env", "# staging\nbase="+srv.URL+"\nx=file-a\ny=file-a\nid=7\n")
	b := write("b.env", "y=file-b\nz=file-b\n")
	src := "GET {{base}}/?x={{x}}&y={{y}}&z={{z}}&w={{w}}\nHTTP 200\n[Asserts]\njsonpath \"$.id\" == {{id}}\n"
	env := map[string]string{"w": "env", "z": "env", "HURL_VARIABLE_x": "env"}
	getenv := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	call := func(args ...string) (int, string) {
		var errOut strings.Builder
		code := run(context.Background(), append(args, "-stdin", "a.hurl"), strings.NewReader(src), io.Discard, &errOut, getenv, nil)
		return code, errOut.String()
	}

	// -variable beats files, a later file beats an earlier one, files beat
	// the environment, and an unquoted 7 is a number.
	code, errOut := call("--variables-file", a, "--variable", "x=cli", "-variables-file", b)
	if code != 0 || got != "x=cli&y=file-b&z=file-b&w=env" {
		t.Errorf("code %d, query %q, err %s", code, got, errOut)
	}
	// A quoted "7" is a string, so it isn't the number 7.
	if code, _ = call("--variables-file", a, "--variable", `id="7"`); code != 4 {
		t.Errorf("quoted id: code %d, want 4", code)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--variable", "novalue"}, "growl: -variable: missing value for variable novalue\n"},
		{[]string{"--variables-file", write("bad.env", "a=1\nb=\"open\n")}, "growl: " + filepath.Join(dir, "bad.env") + ": line 2: the value of b should end with a double quote\n"},
		{[]string{"--variables-file", filepath.Join(dir, "nope.env")}, "growl: open " + filepath.Join(dir, "nope.env") + ": no such file or directory\n"},
	} {
		if code, errOut = call(tc.args...); code != 1 || errOut != tc.want {
			t.Errorf("%v: code %d, err %q, want %q", tc.args, code, errOut, tc.want)
		}
	}
}
