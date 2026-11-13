package signer

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synthlike/growl/internal/hurl"
)

// script writes a shell script that saves its input per phase to dir and
// prints out.
func script(t *testing.T, dir, request, response string) string {
	t.Helper()
	f := filepath.Join(dir, "sign.sh")
	src := `in=$(cat)
case $in in
*'"phase":"request"'*) printf '%s' "$in" >` + dir + `/request.json; printf '%s' '` + request + `' ;;
*) printf '%s' "$in" >` + dir + `/response.json; printf '%s' '` + response + `' ;;
esac
echo note >&2
`
	if err := os.WriteFile(f, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCommand(t *testing.T) {
	dir := t.TempDir()
	var stderr strings.Builder
	var secrets []string
	c := &Command{
		Cmd: script(t, dir,
			`{"request": {"url": "http://b/y", "headers": [["X-Sig", "s"]], "body_base64": "/w=="}, "state": {"k": "K"}, "secrets": ["K"]}`,
			`{"phase": "ignored", "response": {"body": "plain"}}`),
		File: "api.hurl", Stderr: &stderr, OnSecret: func(s string) { secrets = append(secrets, s) },
	}
	req := hurl.Message{Method: "POST", URL: "http://a/x", Header: http.Header{"B": {"2"}, "A": {"1", "3"}}, Body: []byte("hi\n")}
	out, state, err := c.Sign(context.Background(), 7, req)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := os.ReadFile(filepath.Join(dir, "request.json"))
	want := `{"phase":"request","file":"api.hurl","line":7,"request":{"method":"POST","url":"http://a/x","headers":[["A","1"],["A","3"],["B","2"]],"body":"hi\n"}}`
	if string(in) != want {
		t.Errorf("request input:\n%s\nwant\n%s", in, want)
	}
	if out.Method != "POST" || out.URL != "http://b/y" || out.Header.Get("X-Sig") != "s" || out.Header.Get("A") != "" ||
		string(out.Body) != "\xff" || string(state) != `{"k": "K"}` {
		t.Errorf("signed = %+v, state %s", out, state)
	}
	if strings.Join(secrets, ",") != "K" || stderr.String() != "note\n" {
		t.Errorf("secrets %q, stderr %q", secrets, stderr.String())
	}

	resp := hurl.Message{Status: 200, Header: http.Header{"X-Iv": {"v"}}, Body: []byte{0xff, 0}}
	opened, err := c.Open(context.Background(), 7, out, resp, state)
	if err != nil {
		t.Fatal(err)
	}
	in, _ = os.ReadFile(filepath.Join(dir, "response.json"))
	for _, s := range []string{`"phase":"response"`, `"state":{"k":"K"}`, `"status":200`, `"body_base64":"/wA="`, `"headers":[["X-Iv","v"]]`, `"body_base64":"/w=="`} {
		if !strings.Contains(string(in), s) {
			t.Errorf("response input lacks %s:\n%s", s, in)
		}
	}
	if string(opened.Body) != "plain" || opened.Header.Get("X-Iv") != "v" {
		t.Errorf("opened = %+v", opened)
	}
}

func TestCommandNoState(t *testing.T) {
	c := &Command{Cmd: script(t, t.TempDir(), `{"state": null}`, ``)}
	out, state, err := c.Sign(context.Background(), 1, hurl.Message{Method: "GET", URL: "http://a/", Header: http.Header{"A": {"1"}}})
	if err != nil || state != nil || out.Header.Get("A") != "1" {
		t.Errorf("got %+v, %s, %v", out, state, err)
	}
}

func TestCommandErrors(t *testing.T) {
	ctx := context.Background()
	m := hurl.Message{Method: "GET", URL: "http://a/", Header: http.Header{}}
	for cmd, want := range map[string]string{
		`echo "no key" >&2; exit 2`:                           "exit status 2: no key",
		`echo 'not json'`:                                     "request phase: reading its output: ",
		`echo '{"request": {"headers": [["a"]]}}'`:            `request: want each header as [name, value], got ["a"]`,
		`echo '{"request": {"body": "", "body_base64": ""}}'`: "request: want body or body_base64, not both",
	} {
		_, _, err := (&Command{Cmd: cmd}).Sign(ctx, 1, m)
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: got %v, want %s", cmd, err, want)
		}
	}
}
