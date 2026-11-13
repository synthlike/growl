# An unescaped # starts a comment in header and param values.
POST {{base}}/echo
Content-Type: text/plain # not part of it
[Query]
a: x#y
b: x\#y  # escaped
c: "q # v"
{{name}}: 1
`body`
