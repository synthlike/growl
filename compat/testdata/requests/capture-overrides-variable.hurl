# name is Dune as a variable; the capture replaces it.
GET {{base}}/num?v=%22Emma%22
HTTP 200
[Captures]
name: jsonpath "$.v"
GET {{base}}/echo?n={{name}}
