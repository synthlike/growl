GET {{base}}/num?v=%22Dune%22
HTTP 200
[Captures]
s: jsonpath "$.v"
GET {{base}}/shop
HTTP 200
[Asserts]
jsonpath "$.name" == {{s}}
jsonpath "$.name" == "{{s}}"
