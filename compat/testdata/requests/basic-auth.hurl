# Basic auth, with a template in the password.
GET {{base}}/echo
[BasicAuth]
bob: {{name}}:x
