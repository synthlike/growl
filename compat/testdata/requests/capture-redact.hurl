GET {{base}}/shop
HTTP 200
[Captures]
u: jsonpath "$.uuid" redact
POST {{base}}/echo
`{{u}}`
