package hurl

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// Message is a request or a response as a Signer sees it. Requests have
// Method and URL, responses Status. A Host header stands for the request's
// Host.
type Message struct {
	Method string
	URL    string
	Status int
	Header http.Header
	Body   []byte
}

// Signer rewrites each request before it's sent, and optionally its response
// before it's printed and checked: to sign requests, and to encrypt and
// decrypt bodies. Signing is growl's own: hurl has no such hook.
type Signer interface {
	// Sign returns the request to send for the entry at line. A non-nil
	// state asks for Open to be called with the response.
	Sign(ctx context.Context, line int, req Message) (out Message, state []byte, err error)
	// Open returns the response to print and check, given the request
	// that was sent and the state Sign returned.
	Open(ctx context.Context, line int, req, resp Message, state []byte) (Message, error)
}

// sign passes req through r.Signer and returns the request to send, the
// message it was made from, and the signer's state.
func (r *Runner) sign(ctx context.Context, line int, req *http.Request) (*http.Request, Message, []byte, error) {
	m := Message{Method: req.Method, URL: req.URL.String(), Header: req.Header.Clone()}
	if req.Host != "" {
		m.Header.Set("Host", req.Host)
	}
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, m, nil, err
		}
		m.Body = b
	}
	out, state, err := r.Signer.Sign(ctx, line, m)
	if err != nil {
		return nil, m, nil, &Error{Line: line, Msg: "signer: " + err.Error(), Code: ExitRuntime}
	}
	var body io.Reader = http.NoBody
	if len(out.Body) > 0 {
		body = bytes.NewReader(out.Body)
	}
	signed, err := http.NewRequestWithContext(ctx, out.Method, out.URL, body)
	if err != nil {
		return nil, m, nil, &Error{Line: line, Msg: "signer: " + err.Error(), Code: ExitRuntime}
	}
	signed.Header = out.Header.Clone()
	if signed.Header == nil {
		signed.Header = http.Header{}
	}
	if h := signed.Header.Get("Host"); h != "" {
		signed.Host = h
	}
	signed.Header.Del("Host")
	return signed, out, state, nil
}
