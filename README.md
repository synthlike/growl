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

Save this as `octocat.hurl`:

```hurl
GET https://api.github.com/users/octocat
HTTP 200
[Asserts]
jsonpath "$.login" == "octocat"
jsonpath "$.type" == "User"
jsonpath "$.public_repos" > 0
```

The first line is the request. Everything after it describes the response
you expect:

- `HTTP 200`: the status must be 200.
- `jsonpath "$.login" == "octocat"`: the JSON body's `login` field must be
  `"octocat"`. `$` is the whole body, and `.login` is a field in it.
- `jsonpath "$.type" == "User"`: it's a user, not an organization.
- `jsonpath "$.public_repos" > 0`: the user has at least one public repo.

Run it:

```console
$ growl octocat.hurl
GET https://api.github.com/users/octocat
HTTP/2.0 200 OK  417ms
{
  "login": "octocat",
  ...
  "type": "User",
  ...
  "public_repos": 8,
  ...
}
```

growl prints the request, the status with its timing, and the body, with JSON
indented. All the checks passed, so growl exits 0.

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

`owner: jsonpath "$.owner.login"` saves the owner's name as `owner`, and the
second request uses it as `{{owner}}`. `count` checks how many items a list
has: here, that the repository has at least one topic.

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
| `-variable name=value` | Define a variable; repeat for more |
| `-variables-file file` | Read `name=value` lines from a file; repeat for more |
| `-stdin` | Read the file's text from stdin; the argument only names it (for unsaved editor buffers) |
| `-i` | Also print response headers |
| `-timeout d` | Give up on a request after `d`, such as `30s` (default: never) |
| `-signer cmd` | Sign each request (and decrypt its response) with a command; default `$GROWL_SIGNER` |

## Variables and secrets

`{{name}}` fills in a variable. Variables come from the command line, from a
variables file, or from the environment:

```hurl
GET {{API_URL}}/me
Authorization: Bearer {{API_TOKEN}}
HTTP 200
```

```sh
growl -variable API_URL=http://localhost:8080 me.hurl
growl -variables-file staging.env me.hurl
API_URL=http://localhost:8080 growl me.hurl
```

A variables file holds one `name=value` per line. Blank lines and lines
starting with `#` are skipped:

```sh
# staging.env
API_URL=https://staging.example.com
ORDER_ID="1042"
```

An unquoted value takes the type it looks like, so `7` equals the number 7
in a check. In double quotes it stays a string. When a name is defined in
several places, a capture wins, then `-variable`, then the variables files
(the last one first), then the environment: `GROWL_VARIABLE_name`,
`HURL_VARIABLE_name`, then plain `name`. Commands work the same way with
real hurl, which takes the same flags.

Keep secrets out of variables files. Pass them through the environment
instead, where your shell, CI or a password manager can provide them.

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

## Logging in and getting tokens

`[BasicAuth]` sends a user and password, and `[FormParams]` sends a form.
Together they make an OAuth client-credentials token request:

```hurl
POST {{AUTH_URL}}/oauth/token
[BasicAuth]
{{CLIENT_ID}}: {{CLIENT_SECRET}}
[FormParams]
grant_type: client_credentials
scope: orders:read
HTTP 200
[Captures]
token: jsonpath "$.access_token" redact

GET {{API_URL}}/orders
Authorization: Bearer {{token}}
HTTP 200
```

The form goes out as `application/x-www-form-urlencoded`, unless you set
a Content-Type. A request with `[FormParams]` can't also have a body.
`[BasicAuth]` adds to an `Authorization` header you write, rather than
replacing it, as in hurl.

## Signing requests

Many APIs want each request signed (HMAC or RSA over the method, path,
timestamp and body), or the body encrypted (AES, with the key wrapped in
RSA). Each API defines its own scheme, so growl doesn't build any in. A
command you supply does the signing:

```sh
HMAC_SECRET=… growl -signer examples/signers/hmac-sha256.sh api.hurl
```

Before each request, the command gets the request as JSON on stdin. It
writes the request to send on stdout:

```json
{"phase": "request", "file": "api.hurl", "line": 12,
 "request": {"method": "POST", "url": "https://api.example.com/orders?dry=true",
             "headers": [["Content-Type", "application/json"]],
             "body": "{\"sku\": \"w\"}"}}
```

```json
{"request": {"headers": [["Content-Type", "application/json"],
                         ["X-Signature", "…"]]}}
```

Fields left out stay as they were. `headers`, when given, replaces them
all. Other fields are ignored, so `jq '.request.headers += [["X-Sig", $s]]'`
on the input works. A body that isn't UTF-8 travels as `body_base64`.

To decrypt responses, return a `state` too, such as the AES key. growl then
calls the command again with `"phase": "response"`, the request as sent,
the `state` and the `response` (`status`, `headers`, body). It answers with
`{"response": {…}}`, and growl prints and checks that response instead.
List any values to hide from the output under `"secrets"`.

If the command exits non-zero, the request fails with what it wrote to
stderr. In `examples/signers` (they need jq and openssl):

| Script | What it does |
| --- | --- |
| `hmac-sha256.sh` | HMAC-SHA256 of method, path, timestamp and body hash, in `X-Signature` |
| `rsa-sha256.sh` | The same string, signed with an RSA private key |
| `aes-rsa.sh` | AES-256 body with an RSA-OAEP-wrapped key; decrypts the response |

Copy one and change it to match your API. In hiss, set `GROWL_SIGNER` in
an environment's script to sign every request in that environment.

Signing is growl's own. The files stay plain hurl, but under hurl they go
out unsigned.

## What's supported

| Area | Supported |
| --- | --- |
| Requests | any method, headers, `[Query]`, `[FormParams]`, `[BasicAuth]`, JSON bodies, ```` ``` ```` and ```` ```json ```` blocks, `` `oneline` `` bodies |
| Responses | `HTTP 200`, `HTTP/2 200`, `HTTP *`, expected header lines under it |
| Queries | `status`, `header "Name"`, `jsonpath "$..."`, `body`, `duration` (ms), and the `count` filter |
| Checks | `==` `!=` `>` `>=` `<` `<=` `startsWith` `endsWith` `contains` `matches` `exists` `isEmpty`, the type checks (`isString`, `isInteger`, `isIsoDate`, `isUuid`, …), and `not` before any of them |
| Values | `"strings"`, numbers, `true`, `false`, `null`, `/regex/`, `{{variables}}` |
| Captures | `name: query`, with `count` and `redact` |
| JSONPath | `$.a.b`, `$['a']`, `[0]`, `[-1]`, `[*]`, `.*`, `..name` |

It behaves like hurl in the details too. Redirects aren't followed. JSON bodies
are sent as `application/json` unless you set a type. A header with several
values is a list. A captured `"7"` doesn't equal `7`. Numbers compare exactly,
so `9.50 == 9.5`. In header and parameter values, `#` starts a comment;
write `\#` for a literal one.

**Not supported:** XML, HTML and XPath; cookies; multipart bodies;
`[Options]`; filters other than `count`; JSONPath filters and slices;
`file,` and `base64,` bodies. growl stops with an error naming the
unsupported feature and its line. It never quietly skips part of a file.

## Differences from hurl

- Plain environment variables work, as well as `GROWL_VARIABLE_name`. hurl
  reads only `HURL_VARIABLE_name`, `--variable` and `--variables-file`.
- `-line`, `-to` and `-stdin` are growl's own options.
- `-signer` and `GROWL_SIGNER` are growl's own. hurl has no signing hook.
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

For URLs and tokens, use hiss's environments (`[env]` sections with a
`source` script). The active environment's script runs before each request,
so `:env staging` points every `.hurl` file at staging. The script can fetch
secrets fresh each time, for example with `op read`.

## Staying compatible with hurl

`compat/` runs a set of requests and checks through real hurl and through
growl against the same local server, and fails wherever they disagree. Run
`go test ./compat/` with hurl installed; without hurl, it's skipped. Last
checked against hurl 8.1.0.

## License

MIT, see [LICENSE](LICENSE).
