# A capture replaces a --variable of the same name.
GET {{base}}/num?v=%22captured%22
HTTP 200
[Captures]
v: jsonpath "$.v"
GET {{base}}/echo?v={{v}}
