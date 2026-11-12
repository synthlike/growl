# growl

Plain-text HTTP requests you can read, run and check.

Saved `curl` commands tend to end up inside shell scripts, with parameters
passed as arguments, quoting to untangle and `jq` piped on the end. By then you
can't tell what's actually being sent. growl files are the request: method,
URL, headers and body, then what the response should look like.

growl understands a subset of [Hurl](https://hurl.dev)'s format, focused on
HTTP and JSON. It's built to run one request at a time from your editor, and
whole files in CI. Files growl accepts also run unchanged under real hurl.

## A first request

Save this as `go.hurl`:

```hurl
GET https://api.github.com/repos/golang/go/languages
HTTP 200
[Asserts]
jsonpath "$.Go" > 1000000
jsonpath "$.*" count > 3
```

and run it:

```console
$ growl go.hurl
GET https://api.github.com/repos/golang/go/languages
HTTP/2.0 200 OK  396ms
{
  "Go": 58192318,
  "Assembly": 3298169,
  ...
}
```

growl prints the request, the status with its timing, and the body, with JSON
indented. The lines under `HTTP 200` are checks. When they all pass, growl
exits 0.

## Chaining requests

A request can capture values from its response for the ones after it. This
`repo.hurl` finds a repository's owner, then looks the owner up:

```hurl
GET https://api.github.com/repos/golang/go
HTTP 200
[Captures]
owner: jsonpath "$.owner.login"
[Asserts]
jsonpath "$.name" == "go"
jsonpath "$.archived" == false
jsonpath "$.topics" count > 0

GET https://api.github.com/users/{{owner}}
HTTP 200
[Asserts]
jsonpath "$.type" == "Organization"
```

When a check fails, growl prints the file, line, check and the value it got
instead, then exits 4. Every failing check of a request is reported, not just
the first. Change lines 7 and 8 to expect `true` and `> 100`, and:

```console
$ growl repo.hurl
...
repo.hurl:7: jsonpath "$.archived" == true: got false
repo.hurl:8: jsonpath "$.topics" count > 100: got 4
```

Editors that jump to `file:line` take you straight to the check.

## Running part of a file

```sh
growl api.hurl              # every request, in order
growl -line 12 api.hurl     # only the request around line 12
growl -to 12 api.hurl       # every request up to and including that one
```

`-line` is what an editor binds to a key: put the cursor on a request and run
it alone. A request that needs an earlier capture says so:

```console
$ growl -line 10 repo.hurl
repo.hurl:10: undefined variable owner (captured by the request at line 1; run up to here with -to)
```

| Flag | What it does |
| --- | --- |
| `-line n` | Run only the request around line `n` |
| `-to n` | Run the requests up to and including the one around line `n` |
| `-stdin` | Read the file's text from stdin; the argument only names it (for unsaved editor buffers) |
| `-i` | Also print response headers |
| `-timeout d` | Give up on a request after `d`, such as `30s` (default: never) |

## Variables and secrets

`{{name}}` reads from the environment: `GROWL_VARIABLE_name` first, then
`HURL_VARIABLE_name`, then plain `name`. So the variables your shell or
`.env` script already exports just work:

```hurl
GET {{API_URL}}/me
Authorization: Bearer {{API_TOKEN}}
HTTP 200
```

A variable that isn't set is an error, never an empty string. `{{newUuid}}`
and `{{newDate}}` are built in, for request IDs and idempotency keys.

growl prints secrets as `***`: the values of environment variables whose
names look secret (`*TOKEN*`, `*SECRET*`, `*PASSW*`, `*API_KEY*`…), and
values captured with `redact`:

```hurl
POST {{API_URL}}/login
{"user": "{{API_USER}}", "password": "{{API_PASSWORD}}"}
HTTP 200
[Captures]
session: jsonpath "$.token" redact
```

## What's supported

| Area | Supported |
| --- | --- |
| Requests | any method, headers, `[Query]`, JSON bodies, ```` ``` ```` and ```` ```json ```` blocks, `` `oneline` `` bodies |
| Responses | `HTTP 200`, `HTTP/2 200`, `HTTP *`, expected header lines under it |
| Queries | `status`, `header "Name"`, `jsonpath "$..."`, `body`, `duration` (ms), and the `count` filter |
| Checks | `==` `!=` `>` `>=` `<` `<=` `startsWith` `endsWith` `contains` `matches` `exists` `isEmpty`, the type checks (`isString`, `isInteger`, `isIsoDate`, `isUuid`, …), and `not` before any of them |
| Values | `"strings"`, numbers, `true`, `false`, `null`, `/regex/`, `{{variables}}` |
| Captures | `name: query`, with `count` and `redact` |
| JSONPath | `$.a.b`, `$['a']`, `[0]`, `[-1]`, `[*]`, `.*`, `..name` |

It behaves like hurl in the details too. Redirects aren't followed. JSON bodies
are sent as `application/json` unless you set a type. A header with several
values is a list. A captured `"7"` doesn't equal `7`. Numbers compare exactly,
so `9.50 == 9.5`.

**Not supported:** XML, HTML and XPath; cookies; form and multipart bodies;
`[Options]`; filters other than `count`; JSONPath filters and slices;
`file,` and `base64,` bodies. growl stops with an error naming the
unsupported feature and its line. It never quietly skips part of a file.

## Differences from hurl

- Plain environment variables work, as well as `GROWL_VARIABLE_name`. hurl
  reads only `HURL_VARIABLE_name` and `--variable`.
- `-line`, `-to` and `-stdin` are growl's own options.
- Output: growl always prints each request and response, and hides secrets.
  Exit codes match hurl: 1 usage, 2 parse, 3 runtime, 4 assert.

## Install

```sh
go install github.com/synthlike/growl/cmd/growl@latest
```

growl needs Go 1.27 or later and uses only the standard library.

## Using it from hiss

In [hiss](https://github.com/synthlike/hiss), add a rule to `.hissrun`.
Pressing `enter` on a request then runs it, using the buffer as it is on
screen, unsaved edits included:

```ini
[hurl]
match = *.hurl
input = file
cmd   = growl -stdin -line "$line" "$file"
```

## Staying compatible with hurl

`compat/` runs a set of requests and checks through real hurl and through
growl against the same local server, and fails wherever they disagree. Run
`go test ./compat/` with hurl installed; without hurl, it's skipped. Last
checked against hurl 8.1.0.

## License

MIT, see [LICENSE](LICENSE).
