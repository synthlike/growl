GET {{base}}/shop
HTTP 200
[Asserts]
jsonpath "$.id" == {{n}}
jsonpath "$.id" == {{f}}
jsonpath "$.id" != {{s}}
jsonpath "$.ok" == {{t}}
jsonpath "$.name" != {{t}}
