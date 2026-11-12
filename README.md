# growl

A small Go subset of [Hurl](https://hurl.dev) for HTTP and JSON: requests
written as plain text, with the response they should get. Made to run one
request at a time from an editor, and whole files in CI.

```hurl
POST {{API_URL}}/orders
Authorization: Bearer {{API_TOKEN}}
{"sku": "demo-widget", "quantity": 1}
HTTP 201
Location: /orders/1
```

```sh
go install github.com/synthlike/growl/cmd/growl@latest
growl orders.hurl            # every request, in order
growl -line 3 orders.hurl    # only the request around line 3
```

Every growl file is meant to run unchanged under real hurl.

## What it supports

- Requests: method and URL, headers, `[Query]`, JSON bodies, ```` ``` ````
  text bodies and `` `oneline` `` bodies. JSON bodies get
  `Content-Type: application/json` unless you set one.
- Responses: `HTTP 200`, `HTTP/2 200`, `HTTP *`, and header lines under it,
  which must match.
- Templates: `{{name}}` reads `GROWL_VARIABLE_name`, then `HURL_VARIABLE_name`,
  then `name` from the environment. Real hurl reads only `HURL_VARIABLE_name`. `{{newUuid}}` and `{{newDate}}`
  are built in. An undefined variable is an error, never an empty string.
- Redirects aren't followed, as in hurl.

Anything else in the Hurl format is an error that names the feature and its
line. growl never ignores it. `[Asserts]` with `jsonpath`, and `[Captures]`,
come next.

## Output

Each request prints its line, the status with timing, and the body (JSON
indented). `-i` adds response headers. Values of environment variables whose
names look secret (`*TOKEN*`, `*SECRET*`, `*PASSW*`, `*API_KEY*`, …) show as
`***`.

Problems print as `file:line: message`. Exit codes follow hurl: 1 usage,
2 parse, 3 runtime, 4 assert.

## In hiss

`-stdin` reads the text from stdin, so unsaved edits count; the file argument
only names it:

```ini
[hurl]
match = *.hurl
input = file
cmd   = growl -stdin -line "$line" "$file"
```

## License

MIT, see [LICENSE](LICENSE).
