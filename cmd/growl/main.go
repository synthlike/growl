// Command growl runs the requests in a Hurl file, or the one at -line.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/synthlike/growl/internal/hurl"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.LookupEnv, os.Environ())
	stop()
	os.Exit(code)
}

const usage = `usage: growl [-line n] [-stdin] [-i] [-timeout d] file.hurl

Runs the requests in a Hurl file in order, or only the one at -line.
{{name}} reads GROWL_VARIABLE_name, then HURL_VARIABLE_name, then name
from the environment.
Exit codes: 1 usage, 2 parse, 3 runtime, 4 assert.

`

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer,
	getenv func(string) (string, bool), environ []string) int {
	fs := flag.NewFlagSet("growl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { io.WriteString(stderr, usage); fs.PrintDefaults() }
	line := fs.Int("line", 0, "run only the request at this 1-based line")
	fromStdin := fs.Bool("stdin", false, "read the file's text from stdin; the file argument only names it")
	headers := fs.Bool("i", false, "print response headers")
	timeout := fs.Duration("timeout", 0, "per-request timeout, such as 30s (default none)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 1
	}
	name := fs.Arg(0)

	var src []byte
	var err error
	if *fromStdin {
		src, err = io.ReadAll(stdin)
	} else {
		src, err = os.ReadFile(name)
	}
	if err != nil {
		fmt.Fprintf(stderr, "growl: %v\n", err)
		return 1
	}

	out := &redactor{w: stdout, secrets: secrets(environ)}
	errOut := &redactor{w: stderr, secrets: out.secrets}
	report := func(err error) int {
		errs := []error{err}
		if j, ok := err.(interface{ Unwrap() []error }); ok {
			errs = j.Unwrap()
		}
		code := 0
		for _, err := range errs {
			c := hurl.ExitRuntime
			if e, ok := errors.AsType[*hurl.Error](err); ok {
				fmt.Fprintf(errOut, "%s:%d: %s\n", name, e.Line, e.Msg)
				c = e.Code
			} else {
				fmt.Fprintf(errOut, "%s: %v\n", name, err)
			}
			code = max(code, c)
		}
		return code
	}

	es, err := hurl.Parse(string(src))
	if err != nil {
		return report(err)
	}
	if len(es) == 0 {
		fmt.Fprintf(errOut, "%s: no requests\n", name)
		return hurl.ExitParse
	}
	if *line > 0 {
		k := hurl.Pick(es, *line)
		es = es[k : k+1]
	}

	r := &hurl.Runner{
		Client:  hurl.NewClient(*timeout),
		Out:     out,
		Headers: *headers,
		Lookup: func(v string) (string, bool) {
			for _, k := range []string{"GROWL_VARIABLE_" + v, "HURL_VARIABLE_" + v, v} {
				if s, ok := getenv(k); ok {
					return s, true
				}
			}
			return "", false
		},
	}
	for i, e := range es {
		if i > 0 {
			io.WriteString(out, "\n")
		}
		if err := r.Run(ctx, e); err != nil {
			return report(err)
		}
	}
	return 0
}

var secretRe = regexp.MustCompile(`(?i)token|secret|passw|api_?key|private|credential`)

// secrets returns the values of environment variables whose names look
// secret, so output can hide them.
func secrets(environ []string) []string {
	var vs []string
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if len(v) >= 4 && secretRe.MatchString(k) {
			vs = append(vs, v)
		}
	}
	return vs
}

// redactor replaces secret values with *** in what it writes. Callers write
// whole lines or bodies, so a secret isn't split across writes.
type redactor struct {
	w       io.Writer
	secrets []string
}

func (r *redactor) Write(p []byte) (int, error) {
	s := string(p)
	for _, v := range r.secrets {
		s = strings.ReplaceAll(s, v, "***")
	}
	if _, err := io.WriteString(r.w, s); err != nil {
		return 0, err
	}
	return len(p), nil
}
