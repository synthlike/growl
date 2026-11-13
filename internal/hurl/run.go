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

// Runner sends entries and checks their responses. Values captured by one
// entry are available to the entries it runs after it.
type Runner struct {
	Client  *http.Client // should not follow redirects, like hurl
	Lookup  Lookup       // environment variables, after Vars
	Out     io.Writer
	Headers bool // print response headers

	// Vars holds variables from --variable and --variables-file, as
	// ParseVariable and ParseVariables return them. They win over Lookup.
	Vars map[string]any

	// CapturedAt maps names captured by entries that won't run to the
	// line of the entry, for the undefined-variable hint.
	CapturedAt map[string]int
	// OnSecret receives each value captured with redact, before it can be
	// printed, so output can hide it.
	OnSecret func(string)

	captures map[string]any
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

// value finds a template variable as hurl does: a capture first, then
// Vars, then the environment.
func (r *Runner) value(name string) (any, bool) {
	if v, ok := r.captures[name]; ok {
		return v, true
	}
	if v, ok := r.Vars[name]; ok {
		return v, true
	}
	if s, ok := r.Lookup(name); ok {
		return fromEnv(s), true
	}
	return nil, false
}

func (r *Runner) hint(name string) string {
	if line, ok := r.CapturedAt[name]; ok {
		return fmt.Sprintf("captured by the request at line %d; run up to here with -to", line)
	}
	return fmt.Sprintf("pass --variable %[1]s=… or set GROWL_VARIABLE_%[1]s, HURL_VARIABLE_%[1]s or %[1]s", name)
}

// Run sends e, prints the response, takes its captures and checks it.
func (r *Runner) Run(ctx context.Context, e Entry) error {
	req, err := r.request(ctx, e)
	if err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "%s %s\n", req.Method, req.URL)
	start := time.Now()
	hr, err := r.Client.Do(req)
	if err != nil {
		return &Error{Line: e.Line, Msg: err.Error(), Code: ExitRuntime}
	}
	defer hr.Body.Close()
	data, err := io.ReadAll(hr.Body)
	if err != nil {
		return &Error{Line: e.Line, Msg: "reading the response: " + err.Error(), Code: ExitRuntime}
	}
	resp := &response{Response: hr, data: data, dur: time.Since(start)}

	// Captures come first: redacted values must be hidden before the body
	// is printed, and asserts may use them.
	var captured map[string]any
	var capErr error
	if e.Response != nil {
		captured, capErr = r.capture(e.Response.Captures, resp)
	}

	fmt.Fprintf(r.Out, "%s %s  %s\n", hr.Proto, hr.Status, resp.dur.Round(time.Millisecond))
	if r.Headers {
		for _, k := range slices.Sorted(maps.Keys(hr.Header)) {
			for _, v := range hr.Header[k] {
				fmt.Fprintf(r.Out, "%s: %s\n", k, v)
			}
		}
	}
	printBody(r.Out, hr.Header.Get("Content-Type"), data)
	if e.Response == nil {
		return nil
	}
	if err := r.implicit(e.Response, hr); err != nil {
		return err
	}
	if capErr != nil {
		return capErr
	}
	if r.captures == nil {
		r.captures = map[string]any{}
	}
	maps.Copy(r.captures, captured)
	return r.asserts(e.Response.Asserts, resp)
}

func (r *Runner) request(ctx context.Context, e Entry) (*http.Request, error) {
	u, err := expand(e.URL, e.Line, r)
	if err != nil {
		return nil, err
	}
	if len(e.Query) > 0 {
		pu, err := url.Parse(u)
		if err != nil {
			return nil, &Error{Line: e.Line, Msg: err.Error(), Code: ExitRuntime}
		}
		// Append to the URL's own query, in order, with spaces as %20: as hurl does.
		q := []string{}
		if pu.RawQuery != "" {
			q = append(q, pu.RawQuery)
		}
		for _, p := range e.Query {
			k, err := expand(p.Name, p.Line, r)
			if err != nil {
				return nil, err
			}
			v, err := expand(p.Value, p.Line, r)
			if err != nil {
				return nil, err
			}
			q = append(q, queryEscape(k)+"="+queryEscape(v))
		}
		pu.RawQuery = strings.Join(q, "&")
		u = pu.String()
	}
	var body io.Reader
	if e.Body != nil {
		text, err := expand(e.Body.Text, e.Body.Line, r)
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
		v, err := expand(h.Value, h.Line, r)
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

// response is a received response that queries read from.
type response struct {
	*http.Response
	data    []byte
	dur     time.Duration
	doc     any
	docErr  error
	decoded bool
}

// query returns what q selects, none{} for nothing, or a reason it can't.
func (x *response) query(q Query) (any, string) {
	var got any = none{}
	switch q.Kind {
	case "status":
		got = json.Number(strconv.Itoa(x.StatusCode))
	case "duration":
		got = json.Number(strconv.FormatInt(x.dur.Milliseconds(), 10))
	case "body":
		got = string(x.data)
	case "header": // several values make a list, as in hurl
		switch vs := x.Header.Values(q.Arg); len(vs) {
		case 0:
		case 1:
			got = vs[0]
		default:
			l := make([]any, len(vs))
			for i, v := range vs {
				l[i] = v
			}
			got = l
		}
	case "jsonpath":
		if !x.decoded {
			x.doc, x.docErr = decodeJSON(x.data)
			x.decoded = true
		}
		if x.docErr != nil {
			return nil, "the body is not JSON: " + x.docErr.Error()
		}
		// Selecting nothing is none, even for a wildcard, as in hurl.
		switch nodes := q.Path.eval(x.doc); {
		case len(nodes) == 0:
		case q.Path.definite:
			got = nodes[0]
		default:
			got = nodes
		}
	}
	if q.Count {
		l, ok := got.([]any)
		if !ok {
			return nil, "count needs a list, got " + show(got)
		}
		got = json.Number(strconv.Itoa(len(l)))
	}
	return got, ""
}

// capture evaluates an entry's captures against its response.
func (r *Runner) capture(cs []Capture, resp *response) (map[string]any, error) {
	out := map[string]any{}
	for _, c := range cs {
		v, why := resp.query(c.Query)
		if why == "" {
			if _, ok := v.(none); ok {
				why = "selects nothing"
			}
		}
		if why != "" {
			return nil, &Error{Line: c.Line, Msg: c.Text + ": " + why, Code: ExitRuntime}
		}
		if c.Redact && r.OnSecret != nil {
			if s, err := render(v); err == nil && s != "" {
				r.OnSecret(s)
			}
		}
		out[c.Name] = v
	}
	return out, nil
}

// implicit checks the HTTP line: version, status and header lines,
// reporting every failure.
func (r *Runner) implicit(want *Response, resp *http.Response) error {
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
		v, err := expand(h.Value, h.Line, r)
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
	return errors.Join(errs...)
}

// asserts checks [Asserts], reporting every failure.
func (r *Runner) asserts(as []Assert, resp *response) error {
	var errs []error
	for _, a := range as {
		var w any
		if a.Want != nil {
			var err error
			if w, err = a.want(r); err != nil {
				return err
			}
		}
		got, why := resp.query(a.Query)
		switch {
		case why != "":
			errs = append(errs, &Error{Line: a.Line, Msg: a.Text + ": " + why, Code: ExitAssert})
		case !a.check(got, w):
			errs = append(errs, &Error{Line: a.Line, Msg: a.Text + ": got " + show(got), Code: ExitAssert})
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

// queryEscape escapes a query key or value with spaces as %20.
func queryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
