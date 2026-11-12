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
