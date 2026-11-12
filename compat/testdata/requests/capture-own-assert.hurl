GET {{base}}/shop
HTTP 200
[Captures]
id: jsonpath "$.id"
[Asserts]
jsonpath "$.id" == {{id}}
