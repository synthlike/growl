# The string "7" doesn't equal the number 7.
GET {{base}}/num?v=%227%22
HTTP 200
[Captures]
s: jsonpath "$.v"
GET {{base}}/shop
HTTP 200
[Asserts]
jsonpath "$.id" == {{s}}
