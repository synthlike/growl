# How captured numbers render. -0.50 is left out: hurl 8.1 renders it -0.5.0.
GET {{base}}/num?v=9.50
HTTP 200
[Captures]
a: jsonpath "$.v"
GET {{base}}/num?v=1e3
HTTP 200
[Captures]
b: jsonpath "$.v"
GET {{base}}/num?v=1e-7
HTTP 200
[Captures]
c: jsonpath "$.v"
GET {{base}}/num?v=12345678901234567890
HTTP 200
[Captures]
d: jsonpath "$.v"
GET {{base}}/num?v=1.0
HTTP 200
[Captures]
e: jsonpath "$.v"
GET {{base}}/num?v=null
HTTP 200
[Captures]
f: jsonpath "$.v"
GET {{base}}/num?v=true
HTTP 200
[Captures]
g: jsonpath "$.v"
POST {{base}}/echo
`{{a}}|{{b}}|{{c}}|{{d}}|{{e}}|{{f}}|{{g}}`
