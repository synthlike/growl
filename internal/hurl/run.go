package hurl

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
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
	dur := time.Since(start)
	fmt.Fprintf(r.Out, "%s %s  %s\n", resp.Proto, resp.Status, dur.Round(time.Millisecond))
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
	return r.check(e.Response, resp, data, dur)
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

// check compares a response with what the entry expects: version, status,
// implicit headers, then [Asserts]. It reports every failure, joined.
func (r *Runner) check(want *Response, resp *http.Response, data []byte, dur time.Duration) error {
	var errs []error
	fail := func(line int, format string, args ...any) {
		errs = append(errs, &Error{Line: line, Msg: fmt.Sprintf(format, args...), Code: ExitAssert})
	}
	if want.Version != "" {
		got := strings.TrimSuffix(strings.TrimPrefix(resp.Proto, "HTTP/"), ".0")
		if want.Version == "1.0" && got == "1" {
			got = "1.0"
		}
		if got != want.Version {
			fail(want.Line, "HTTP/%s: got %s", want.Version, resp.Proto)
		}
	}
	if want.Status != 0 && resp.StatusCode != want.Status {
		fail(want.Line, "HTTP %d: got %d", want.Status, resp.StatusCode)
	}
	for _, h := range want.Headers {
		v, err := expand(h.Value, h.Line, r.Lookup)
		if err != nil {
			return err
		}
		got := resp.Header.Values(h.Name)
		if !slices.Contains(got, v) {
			if len(got) == 0 {
				fail(h.Line, "header %s: want %q, got none", h.Name, v)
			} else {
				fail(h.Line, "header %s: want %q, got %q", h.Name, v, strings.Join(got, ", "))
			}
		}
	}

	var doc any
	var docErr error
	decoded := false
	for _, a := range want.Asserts {
		var w any
		if a.Want != nil {
			var err error
			if w, err = a.want(r.Lookup); err != nil {
				return err
			}
		}
		var got []any // a header can have several values; any may pass
		switch a.Query {
		case "status":
			got = []any{json.Number(strconv.Itoa(resp.StatusCode))}
		case "duration":
			got = []any{json.Number(strconv.FormatInt(dur.Milliseconds(), 10))}
		case "body":
			got = []any{string(data)}
		case "header":
			for _, v := range resp.Header.Values(a.Arg) {
				got = append(got, v)
			}
		case "jsonpath":
			if !decoded {
				doc, docErr = decodeJSON(data)
				decoded = true
			}
			if docErr != nil {
				fail(a.Line, "%s: the body is not JSON: %v", a.Text, docErr)
				continue
			}
			nodes := a.Path.eval(doc)
			switch {
			case !a.Path.definite:
				got = []any{nodes}
			case len(nodes) == 1:
				got = []any{nodes[0]}
			}
		}
		if len(got) == 0 {
			got = []any{none{}}
		}
		if a.Count {
			for i, g := range got {
				switch v := g.(type) {
				case []any:
					got[i] = json.Number(strconv.Itoa(len(v)))
				case map[string]any:
					got[i] = json.Number(strconv.Itoa(len(v)))
				case none:
				default:
					fail(a.Line, "%s: count needs a list or object, got %s", a.Text, show(v))
					got = nil
				}
			}
			if got == nil {
				continue
			}
		}
		ok := slices.ContainsFunc(got, func(g any) bool { return a.check(g, w) })
		if a.Not && len(got) > 1 { // not: no value may pass the positive test
			ok = !slices.ContainsFunc(got, func(g any) bool { return !a.check(g, w) })
		}
		if !ok {
			shown := make([]string, len(got))
			for i, g := range got {
				shown[i] = show(g)
			}
			fail(a.Line, "%s: got %s", a.Text, strings.Join(shown, ", "))
		}
	}
	return errors.Join(errs...)
}

// decodeJSON decodes a whole body, keeping numbers exact.
func decodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("data after the JSON value")
	}
	return v, nil
}
