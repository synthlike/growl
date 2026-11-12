GET {{base}}/shop
HTTP 200
[Captures]
n: jsonpath "$.tags" count
POST {{base}}/echo
`{{n}}`
