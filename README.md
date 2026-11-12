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
[Asserts]
jsonpath "$.quantity" == 1
jsonpath "$.items" count > 0
```

```sh
go install github.com/synthlike/growl/cmd/growl@latest
growl orders.hurl            # every request, in order
growl -line 3 orders.hurl    # only the request around line 3
```

Every growl file is meant to run unchanged under real hurl.

## What it supports

- Requests: method and URL, headers, `[Query]` (appended to the URL's own
  query), JSON bodies, ```` ``` ```` text bodies and `` `oneline` `` bodies.
  JSON and ```` ```json ```` bodies get `Content-Type: application/json`
  unless you set one.
- Responses: `HTTP 200`, `HTTP/2 200`, `HTTP *`, header lines under it
  (which must match), and `[Asserts]`.
- Asserts: the queries `status`, `header "Name"`, `jsonpath "$.path"`, `body`
  and `duration` (ms); the `count` filter; `not`; the predicates `==` `!=`
  `>` `>=` `<` `<=` `startsWith` `endsWith` `contains` `matches`
  `exists` `isEmpty` `isBoolean` `isNumber` `isInteger` `isFloat` `isString`
  `isList` `isObject` `isIsoDate` `isUuid` `isIpv4` `isIpv6`. Values are
  `"strings"`, numbers, `true`/`false`, `null`, `/regex/` and `{{variables}}`.
  `contains` works on strings and lists (`includes`, deprecated in hurl, on
  lists only). As in hurl, a header with several values is a list, a query
  that selects nothing is none, where every predicate fails and every `not`
  one passes, and `count` takes lists only.
- JSONPath: `$`, `.name`, `['name']`, `[0]`, `[-1]`, `.*`, `[*]`, `..name`.
  No filters, slices or unions.
- Templates: `{{name}}` reads `GROWL_VARIABLE_name`, then `HURL_VARIABLE_name`,
  then `name` from the environment; real hurl reads only `HURL_VARIABLE_name`.
  `{{newUuid}}` and `{{newDate}}` are built in. An undefined variable is an
  error, never an empty string.
- Redirects aren't followed, as in hurl.

Anything else in the Hurl format is an error that names the feature and its
line. growl never ignores it. `[Captures]` come next.

## Output

Each request prints its line, the status with timing, and the body (JSON
indented). `-i` adds response headers. Values of environment variables whose
names look secret (`*TOKEN*`, `*SECRET*`, `*PASSW*`, `*API_KEY*`, …) show as
`***`.

Problems print as `file:line: message`, with every failing assert of a
request on its own line. Exit codes follow hurl: 1 usage, 2 parse, 3 runtime,
4 assert.

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
