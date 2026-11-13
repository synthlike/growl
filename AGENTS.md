# growl

A small Go subset of [Hurl](https://hurl.dev): plain-text HTTP requests with
JSON asserts and captures, built to run one request at a time from an editor
(hiss's `enter`) and whole files in CI. Aim for 80/20: HTTP and JSON only.

## Layout

- `cmd/growl`: entry point (`growl [-line n | -to n] [-variable n=v] [-variables-file f] [-signer cmd] [-stdin] [-i] file.hurl`).
- `internal/hurl`: parser (with line numbers), templates, JSONPath, asserts, runner, and the `Signer` hook.
- `internal/signer`: the `-signer` command protocol (JSON on stdin/stdout).
- `examples/signers`: HMAC, RSA and AES+RSA signer scripts (jq, openssl).
- `compat`: runs `testdata` cases through real hurl and growl and fails where
  they disagree; skipped when hurl isn't installed.

## Rules

- Stay a strict subset of Hurl: a file growl accepts must mean the same thing
  to real hurl, except for the documented GROWL_VARIABLE_ and plain-env variable fallbacks
  and `-signer`, which lives outside the file so files stay plain hurl.
  Where hurl silently drops part of a file (a second aliased section), growl
  rejects it instead.
  Unsupported Hurl features fail with an error naming the feature and line;
  never ignore them.
- When adding or changing behavior, add a case to `compat/testdata` and run
  `go test ./compat/` with hurl installed. hurl decides what's right.
- Standard library only. Ask before adding a dependency.
- Verify library APIs with `go doc` before using them; don't rely on memory.
- Errors are `file:line: message`, so editors can jump to them. Exit codes
  follow hurl: 1 usage, 2 parse, 3 runtime, 4 assert.
- Work in small runnable steps: `go vet ./... && go test ./...` passes before each commit.
