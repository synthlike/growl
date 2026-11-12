GET {{base}}/shop
HTTP 200
[Captures]
h: header "Set-Thing"
GET {{base}}/echo?h={{h}}
