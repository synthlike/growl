// Package compat checks growl against real hurl. Each case in testdata runs
// through both against the same local server; the test fails where they
// disagree. It skips when hurl isn't on PATH.
package compat

import (
	"bufio"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synthlike/growl/internal/hurl"
)

const shop = `{"id": 7, "price": 9.50, "name": "Dune", "tags": ["sf", "classic"],
	"owner": null, "ok": true, "made": "2026-11-12T20:00:00Z", "ip": "10.0.0.1",
	"uuid": "8d86c9b4-e813-48e9-a416-f2a63a516d2b", "empty": [], "meta": {"a": 1},
	"big": 12345678901234567890, "one": 1.0}`

// request is what the server saw.
type request struct{ Method, Query, ContentType, Body string }

type server struct {
	*httptest.Server
	mu   sync.Mutex
	last request
}

func newServer() *server {
	s := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/shop", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Thing", "a=1")
		w.Header().Add("Set-Thing", "b=2")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, shop)
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.last = request{r.Method, r.URL.RawQuery, r.Header.Get("Content-Type"), string(b)}
		s.mu.Unlock()
	})
	mux.HandleFunc("/num", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"v": `+r.URL.Query().Get("v")+`}`) // any raw JSON value
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/shop", http.StatusFound)
	})
	s.Server = httptest.NewServer(mux)
	return s
}

// take returns and clears the last request the server saw.
func (s *server) take() request {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.last
	s.last = request{}
	return r
}

func TestMatchesHurl(t *testing.T) {
	bin, err := exec.LookPath("hurl")
	if err != nil {
		t.Skip("hurl is not installed")
	}
	if out, err := exec.Command(bin, "--version").Output(); err == nil {
		t.Log(strings.SplitN(string(out), "\n", 2)[0])
	}
	srv := newServer()
	defer srv.Close()
	vars := map[string]string{"base": srv.URL, "id": "7", "name": "Dune"}
	dir := t.TempDir()

	t.Run("asserts", func(t *testing.T) {
		f, err := os.Open("testdata/asserts.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			a := sc.Text()
			if a == "" || strings.HasPrefix(a, "#") {
				continue
			}
			src := "GET {{base}}/shop\nHTTP 200\n[Asserts]\n" + a + "\n"
			h := runHurl(t, bin, dir, src, vars)
			g := runGrowl(src, vars)
			if h != g {
				t.Errorf("asserts.txt:%d: %s: hurl exits %d, growl %d", n, a, h, g)
			}
		}
	})

	t.Run("variables", func(t *testing.T) {
		files, _ := filepath.Glob("testdata/variables/*.hurl")
		if len(files) == 0 {
			t.Fatal("no variable cases")
		}
		for _, file := range files {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			stem := strings.TrimSuffix(file, ".hurl")
			var extra []string
			if _, err := os.Stat(stem + ".env"); err == nil {
				extra = append(extra, "--variables-file", stem+".env")
			}
			if b, err := os.ReadFile(stem + ".args"); err == nil {
				for _, v := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
					extra = append(extra, "--variable", v)
				}
			}
			h := runHurl(t, bin, dir, string(src), vars, extra...)
			hr := srv.take()
			g := runGrowl(string(src), vars, extra...)
			gr := srv.take()
			if h != g || hr != gr {
				t.Errorf("%s:\nhurl:  exit %d, %+v\ngrowl: exit %d, %+v", file, h, hr, g, gr)
			} else {
				t.Logf("%s: both exit %d, server saw %+v", filepath.Base(file), h, hr)
			}
		}
	})

	t.Run("requests", func(t *testing.T) {
		files, _ := filepath.Glob("testdata/requests/*.hurl")
		if len(files) == 0 {
			t.Fatal("no request cases")
		}
		for _, file := range files {
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			src := string(b)
			h := runHurl(t, bin, dir, src, vars)
			hr := srv.take()
			g := runGrowl(src, vars)
			gr := srv.take()
			if h != g || hr != gr {
				t.Errorf("%s:\nhurl:  exit %d, %+v\ngrowl: exit %d, %+v", file, h, hr, g, gr)
			} else {
				t.Logf("%s: both exit %d, server saw %+v", filepath.Base(file), h, hr)
			}
		}
	})
}

// runHurl runs src with real hurl, with vars as --variable flags and any
// extra arguments, and returns its exit code. HURL_* variables from the
// caller's environment are left out.
func runHurl(t *testing.T, bin, dir, src string, vars map[string]string, extra ...string) int {
	t.Helper()
	file := filepath.Join(dir, "case.hurl")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--no-output"}
	for k, v := range vars {
		args = append(args, "--variable", k+"="+v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args = append(args, extra...)
	cmd := exec.CommandContext(ctx, bin, append(args, file)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HURL_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	err := cmd.Run()
	if e, ok := errors.AsType[*exec.ExitError](err); ok {
		return e.ExitCode()
	}
	if err != nil {
		t.Fatalf("running hurl: %v", err)
	}
	return 0
}

// runGrowl runs src with growl's parser and runner and returns the exit
// code growl would. vars come from the environment; extra holds the same
// --variables-file and --variable arguments hurl gets.
func runGrowl(src string, vars map[string]string, extra ...string) int {
	flags := map[string]any{}
	for i := 0; i+1 < len(extra); i += 2 {
		switch extra[i] {
		case "--variables-file":
			b, err := os.ReadFile(extra[i+1])
			if err != nil {
				return 1
			}
			vs, err := hurl.ParseVariables(string(b))
			if err != nil {
				return 1
			}
			maps.Copy(flags, vs)
		}
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i] == "--variable" {
			name, v, err := hurl.ParseVariable(extra[i+1])
			if err != nil {
				return 1
			}
			flags[name] = v
		}
	}
	code := func(err error) int {
		if e, ok := errors.AsType[*hurl.Error](err); ok {
			return e.Code
		}
		return hurl.ExitRuntime
	}
	es, err := hurl.Parse(src)
	if err != nil {
		return code(err)
	}
	r := &hurl.Runner{
		Client: hurl.NewClient(20 * time.Second),
		Out:    io.Discard,
		Vars:   flags,
		Lookup: func(k string) (string, bool) { v, ok := vars[k]; return v, ok },
	}
	for _, e := range es {
		if err := r.Run(context.Background(), e); err != nil {
			return code(err)
		}
	}
	return 0
}
