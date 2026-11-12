GET {{base}}/shop
HTTP 200
[Captures]
tags: jsonpath "$.tags"
GET {{base}}/echo?t={{tags}}
