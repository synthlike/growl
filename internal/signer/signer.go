// Package signer runs a command to sign requests and decrypt responses for
// growl. The command gets one JSON object on stdin and writes one to stdout.
//
// Before each request it gets
//
//	{"phase": "request", "file": "api.hurl", "line": 12,
//	 "request": {"method": "POST", "url": "https://…",
//	             "headers": [["Content-Type", "application/json"]],
//	             "body": "{…}"}}
//
// and answers with the request to send. Fields it leaves out stay as they
// were; "headers", when given, replaces them all. Other fields are ignored,
// so echoing the input with changes works, as in
// jq '.request.headers += [["X-Signature", "…"]]':
//
//	{"request": {"headers": [["Content-Type", "application/json"],
//	                         ["X-Signature", "…"]]},
//	 "state": {…}, "secrets": ["…"]}
//
// With "state" (anything but null), it's called again once the response is
// in, and answers with the response to print and check:
//
//	{"phase": "response", "file": "api.hurl", "line": 12,
//	 "request": {…as sent…}, "state": {…},
//	 "response": {"status": 200, "headers": [[…]], "body_base64": "…"}}
//
//	{"response": {"headers": [[…]], "body": "{…}"}}
//
// A body that isn't UTF-8 travels as "body_base64" instead of "body", either
// way. "secrets" are values growl hides in its output. A Host header stands
// for the request's Host. A command that exits non-zero fails the request
// with what it wrote to stderr; otherwise its stderr is passed on.
package signer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/synthlike/growl/internal/hurl"
)

// Command is a hurl.Signer that runs a shell command for each call.
type Command struct {
	Cmd      string    // run with sh -c
	File     string    // the .hurl file's name, passed to the command
	Stderr   io.Writer // gets the command's stderr when it succeeds
	OnSecret func(string)
}

var _ hurl.Signer = (*Command)(nil)

// message is a request or response on the wire.
type message struct {
	Method     string      `json:"method,omitempty"`
	URL        string      `json:"url,omitempty"`
	Status     int         `json:"status,omitempty"`
	Headers    *[][]string `json:"headers,omitempty"`
	Body       *string     `json:"body,omitempty"`
	BodyBase64 *string     `json:"body_base64,omitempty"`
}

type input struct {
	Phase    string          `json:"phase"`
	File     string          `json:"file"`
	Line     int             `json:"line"`
	Request  message         `json:"request"`
	Response *message        `json:"response,omitempty"`
	State    json.RawMessage `json:"state,omitempty"`
}

type output struct {
	Request  *message        `json:"request"`
	Response *message        `json:"response"`
	State    json.RawMessage `json:"state"`
	Secrets  []string        `json:"secrets"`
}

func toWire(m hurl.Message) message {
	w := message{Method: m.Method, URL: m.URL, Status: m.Status}
	hs := [][]string{}
	for _, k := range slices.Sorted(maps.Keys(m.Header)) {
		for _, v := range m.Header[k] {
			hs = append(hs, []string{k, v})
		}
	}
	w.Headers = &hs
	if utf8.Valid(m.Body) {
		s := string(m.Body)
		w.Body = &s
	} else {
		s := base64.StdEncoding.EncodeToString(m.Body)
		w.BodyBase64 = &s
	}
	return w
}

// apply returns m with the fields w sets.
func apply(m hurl.Message, w *message) (hurl.Message, error) {
	if w == nil {
		return m, nil
	}
	if w.Method != "" {
		m.Method = w.Method
	}
	if w.URL != "" {
		m.URL = w.URL
	}
	if w.Headers != nil {
		m.Header = http.Header{}
		for _, h := range *w.Headers {
			if len(h) != 2 {
				return m, fmt.Errorf("want each header as [name, value], got %q", h)
			}
			m.Header.Add(h[0], h[1])
		}
	}
	switch {
	case w.Body != nil && w.BodyBase64 != nil:
		return m, errors.New("want body or body_base64, not both")
	case w.Body != nil:
		m.Body = []byte(*w.Body)
	case w.BodyBase64 != nil:
		b, err := base64.StdEncoding.DecodeString(*w.BodyBase64)
		if err != nil {
			return m, fmt.Errorf("body_base64: %v", err)
		}
		m.Body = b
	}
	return m, nil
}

func (c *Command) Sign(ctx context.Context, line int, req hurl.Message) (hurl.Message, []byte, error) {
	out, err := c.call(ctx, input{Phase: "request", File: c.File, Line: line, Request: toWire(req)})
	if err != nil {
		return req, nil, err
	}
	signed, err := apply(req, out.Request)
	if err != nil {
		return req, nil, fmt.Errorf("request: %v", err)
	}
	var state []byte
	if len(out.State) > 0 && string(out.State) != "null" {
		state = out.State
	}
	return signed, state, nil
}

func (c *Command) Open(ctx context.Context, line int, req, resp hurl.Message, state []byte) (hurl.Message, error) {
	w := toWire(resp)
	out, err := c.call(ctx, input{Phase: "response", File: c.File, Line: line, Request: toWire(req), Response: &w, State: state})
	if err != nil {
		return resp, err
	}
	opened, err := apply(resp, out.Response)
	if err != nil {
		return resp, fmt.Errorf("response: %v", err)
	}
	return opened, nil
}

func (c *Command) call(ctx context.Context, in input) (*output, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", c.Cmd)
	cmd.Stdin = bytes.NewReader(b)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%v: %s", err, msg)
		}
		return nil, err
	}
	if c.Stderr != nil && stderr.Len() > 0 {
		c.Stderr.Write(stderr.Bytes())
	}
	var out output
	// Other fields are ignored, so a command can echo its input with changes.
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("%s phase: reading its output: %v", in.Phase, err)
	}
	if c.OnSecret != nil {
		for _, s := range out.Secrets {
			if s != "" {
				c.OnSecret(s)
			}
		}
	}
	return &out, nil
}
