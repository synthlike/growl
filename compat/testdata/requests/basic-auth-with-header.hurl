# An Authorization header and [BasicAuth] are both sent, the header first.
GET {{base}}/echo
Authorization: Bearer t
[BasicAuth]
bob: secret
