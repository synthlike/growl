package hurl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rot13 signs requests with a header, "encrypts" bodies with rot13 and
// "decrypts" responses that say they're encrypted.
type rot13 struct{ opened int }

func rot(b []byte) []byte {
	out := bytes.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return 'a' + (r-'a'+13)%26
		case r >= 'A' && r <= 'Z':
			return 'A' + (r-'A'+13)%26
		}
		return r
	}, b)
	return out
}

func (s *rot13) Sign(_ context.Context, line int, m Message) (Message, []byte, error) {
	if strings.Contains(m.URL, "fail") {
		return m, nil, errors.New("no key")
	}
	m.Header.Set("X-Sig", m.Method+" "+m.Header.Get("Host"))
	m.Header.Set("Host", "signed.example")
	m.Body = rot(m.Body)
	if strings.Contains(m.URL, "plain") {
		return m, nil, nil
	}
	return m, []byte(`{"k":1}`), nil
}

func (s *rot13) Open(_ context.Context, line int, req, resp Message, state []byte) (Message, error) {
	s.opened++
	if string(state) != `{"k":1}` || req.Header.Get("X-Sig") == "" {
		return resp, errors.New("lost state")
	}
	resp.Body = rot(resp.Body)
	resp.Header = http.Header{"X-Opened": {"yes"}}
	return resp, nil
}

func TestRunSigner(t *testing.T) {
	var sig, host, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sig, host, body = r.Header.Get("X-Sig"), r.Host, string(b)
		io.WriteString(w, `{"anzr": "Qhar"}`) // {"name": "Dune"}
	}))
	defer srv.Close()
	s := &rot13{}
	var out strings.Builder
	r := &Runner{Client: NewClient(0), Out: &out, Signer: s, Lookup: func(string) (string, bool) { return "", false }}
	run := func(src string) error {
		es, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		return r.Run(context.Background(), es[0])
	}

	err := run("POST " + srv.URL + "/x\nHost: orig.example\n`hello`\nHTTP 200\nX-Opened: yes\n[Asserts]\njsonpath \"$.name\" == \"Dune\"\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if sig != "POST orig.example" || host != "signed.example" || body != "uryyb" || s.opened != 1 {
		t.Errorf("server saw sig %q host %q body %q; opened %d", sig, host, body, s.opened)
	}
	if !strings.Contains(out.String(), `"name": "Dune"`) {
		t.Errorf("output shows the encrypted body:\n%s", out.String())
	}

	// No state: the response is left alone.
	if err := run("GET " + srv.URL + "/plain\nHTTP 200\n[Asserts]\njsonpath \"$.anzr\" exists\n"); err != nil || s.opened != 1 {
		t.Errorf("plain: %v, opened %d", err, s.opened)
	}

	var e *Error
	if err := run("GET " + srv.URL + "/fail\n"); !errors.As(err, &e) || e.Code != ExitRuntime || e.Line != 1 || e.Msg != "signer: no key" {
		t.Errorf("failing signer: %v", err)
	}
}
