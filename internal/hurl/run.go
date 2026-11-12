package hurl

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Runner sends entries and checks their responses.
type Runner struct {
	Client  *http.Client // should not follow redirects, like hurl
	Lookup  Lookup
	Out     io.Writer
	Headers bool // print response headers
}

// NewClient returns a client that, like hurl by default, doesn't follow
// redirects. A zero timeout means none.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Run sends e, prints the response, and checks it against e.Response.
func (r *Runner) Run(ctx context.Context, e Entry) error {
	req, err := r.request(ctx, e)
	if err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "%s %s\n", req.Method, req.URL)
	start := time.Now()
	resp, err := r.Client.Do(req)
	if err != nil {
		return &Error{Line: e.Line, Msg: err.Error(), Code: ExitRuntime}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return &Error{Line: e.Line, Msg: "reading the response: " + err.Error(), Code: ExitRuntime}
	}
	fmt.Fprintf(r.Out, "%s %s  %s\n", resp.Proto, resp.Status, time.Since(start).Round(time.Millisecond))
	if r.Headers {
		for _, k := range slices.Sorted(maps.Keys(resp.Header)) {
			for _, v := range resp.Header[k] {
				fmt.Fprintf(r.Out, "%s: %s\n", k, v)
			}
		}
	}
	printBody(r.Out, resp.Header.Get("Content-Type"), data)
	if e.Response == nil {
		return nil
	}
	return r.check(e.Response, resp)
}

func (r *Runner) request(ctx context.Context, e Entry) (*http.Request, error) {
	u, err := expand(e.URL, e.Line, r.Lookup)
	if err != nil {
		return nil, err
	}
	if len(e.Query) > 0 {
		pu, err := url.Parse(u)
		if err != nil {
			return nil, &Error{Line: e.Line, Msg: err.Error(), Code: ExitRuntime}
		}
		q := pu.Query()
		for _, p := range e.Query {
			v, err := expand(p.Value, p.Line, r.Lookup)
			if err != nil {
				return nil, err
			}
			q.Add(p.Name, v)
		}
		pu.RawQuery = q.Encode()
		u = pu.String()
	}
	var body io.Reader
	if e.Body != nil {
		text, err := expand(e.Body.Text, e.Body.Line, r.Lookup)
		if err != nil {
			return nil, err
		}
		body = strings.NewReader(text)
	}
	req, err := http.NewRequestWithContext(ctx, e.Method, u, body)
	if err != nil {
		return nil, &Error{Line: e.Line, Msg: err.Error(), Code: ExitRuntime}
	}
	for _, h := range e.Headers {
		v, err := expand(h.Value, h.Line, r.Lookup)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(h.Name, "Host") {
			req.Host = v
			continue
		}
		req.Header.Add(h.Name, v)
	}
	if e.Body != nil && e.Body.JSON && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// check compares a response with what the entry expects.
func (r *Runner) check(want *Response, resp *http.Response) error {
	fail := func(line int, format string, args ...any) error {
		return &Error{Line: line, Msg: fmt.Sprintf(format, args...), Code: ExitAssert}
	}
	if want.Version != "" {
		got := strings.TrimSuffix(strings.TrimPrefix(resp.Proto, "HTTP/"), ".0")
		if want.Version == "1.0" && got == "1" {
			got = "1.0"
		}
		if got != want.Version {
			return fail(want.Line, "HTTP/%s: got %s", want.Version, resp.Proto)
		}
	}
	if want.Status != 0 && resp.StatusCode != want.Status {
		return fail(want.Line, "HTTP %d: got %d", want.Status, resp.StatusCode)
	}
	for _, h := range want.Headers {
		v, err := expand(h.Value, h.Line, r.Lookup)
		if err != nil {
			return err
		}
		got := resp.Header.Values(h.Name)
		if !slices.Contains(got, v) {
			if len(got) == 0 {
				return fail(h.Line, "header %s: want %q, got none", h.Name, v)
			}
			return fail(h.Line, "header %s: want %q, got %q", h.Name, v, strings.Join(got, ", "))
		}
	}
	return nil
}

// printBody writes JSON indented, text as is and binary as a size.
func printBody(w io.Writer, ctype string, data []byte) {
	if len(data) == 0 {
		return
	}
	var buf bytes.Buffer
	if json.Indent(&buf, data, "", "  ") == nil {
		buf.WriteByte('\n')
		w.Write(buf.Bytes())
		return
	}
	if !utf8.Valid(data) {
		fmt.Fprintf(w, "<%d bytes of %s>\n", len(data), cmp.Or(ctype, "binary data"))
		return
	}
	w.Write(data)
	if data[len(data)-1] != '\n' {
		io.WriteString(w, "\n")
	}
}
