// Command growl runs the requests in a Hurl file, or the one at -line.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
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

const usage = `usage: growl [-line n | -to n] [-variable name=value]... [-variables-file file]...
             [-stdin] [-i] [-timeout d] file.hurl

Runs the requests in a Hurl file in order: all of them, only the one at
-line, or the ones up to and including it with -to. Captures carry over
to later requests.
{{name}} is a capture, then -variable, then -variables-file, then
GROWL_VARIABLE_name, HURL_VARIABLE_name or name from the environment.
Exit codes: 1 usage, 2 parse, 3 runtime, 4 assert.

`

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer,
	getenv func(string) (string, bool), environ []string) int {
	fs := flag.NewFlagSet("growl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { io.WriteString(stderr, usage); fs.PrintDefaults() }
	line := fs.Int("line", 0, "run only the request at this 1-based line")
	to := fs.Int("to", 0, "run the requests up to and including the one at this line")
	fromStdin := fs.Bool("stdin", false, "read the file's text from stdin; the file argument only names it")
	headers := fs.Bool("i", false, "print response headers")
	timeout := fs.Duration("timeout", 0, "per-request timeout, such as 30s (default none)")
	var variables, files repeated
	fs.Var(&variables, "variable", "define a variable, name=value (repeatable)")
	fs.Var(&files, "variables-file", "read name=value lines from a file (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *line > 0 && *to > 0 {
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
	vars, err := loadVariables(files, variables)
	if err != nil {
		fmt.Fprintf(stderr, "growl: %v\n", err)
		return 1
	}

	hidden := &secrets{vs: envSecrets(environ)}
	out := &redactor{w: stdout, s: hidden}
	errOut := &redactor{w: stderr, s: hidden}
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
	var capturedAt map[string]int
	switch {
	case *line > 0:
		k := hurl.Pick(es, *line)
		capturedAt = map[string]int{}
		for _, e := range es[:k] {
			if e.Response != nil {
				for _, c := range e.Response.Captures {
					capturedAt[c.Name] = e.Line
				}
			}
		}
		es = es[k : k+1]
	case *to > 0:
		es = es[:hurl.Pick(es, *to)+1]
	}

	r := &hurl.Runner{
		Client:     hurl.NewClient(*timeout),
		Out:        out,
		Headers:    *headers,
		Vars:       vars,
		CapturedAt: capturedAt,
		OnSecret:   func(s string) { hidden.vs = append(hidden.vs, s) },
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

// envSecrets returns the values of environment variables whose names look
// secret, so output can hide them.
func envSecrets(environ []string) []string {
	var vs []string
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if len(v) >= 4 && secretRe.MatchString(k) {
			vs = append(vs, v)
		}
	}
	return vs
}

// secrets are values to hide; redacted captures add to them as a file runs.
type secrets struct{ vs []string }

// redactor replaces secret values with *** in what it writes. Callers write
// whole lines or bodies, so a secret isn't split across writes.
type redactor struct {
	w io.Writer
	s *secrets
}

func (r *redactor) Write(p []byte) (int, error) {
	s := string(p)
	for _, v := range r.s.vs {
		s = strings.ReplaceAll(s, v, "***")
	}
	if _, err := io.WriteString(r.w, s); err != nil {
		return 0, err
	}
	return len(p), nil
}

// repeated is a flag that can be given more than once.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ", ") }
func (r *repeated) Set(s string) error { *r = append(*r, s); return nil }

// loadVariables reads variables files in order, then -variable values on
// top: as in hurl, a -variable wins over a file, and a later value over an
// earlier one.
func loadVariables(files, variables []string) (map[string]any, error) {
	vars := map[string]any{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		vs, err := hurl.ParseVariables(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %v", f, err)
		}
		maps.Copy(vars, vs)
	}
	for _, s := range variables {
		name, v, err := hurl.ParseVariable(s)
		if err != nil {
			return nil, fmt.Errorf("-variable: %v", err)
		}
		vars[name] = v
	}
	return vars, nil
}
