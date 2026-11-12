GET {{base}}/shop
HTTP 200
[Asserts]
jsonpath "$.id" == {{s}}
