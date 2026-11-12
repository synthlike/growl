# The HTTP line fails before the capture is tried.
GET {{base}}/shop
HTTP 201
[Captures]
x: jsonpath "$.nope"
