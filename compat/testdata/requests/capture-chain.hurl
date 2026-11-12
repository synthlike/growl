GET {{base}}/shop
HTTP 200
[Captures]
id: jsonpath "$.id"
book: jsonpath "$.name"
POST {{base}}/echo
[Query]
n: {{id}}
{"id": {{id}}, "book": "{{book}}"}
