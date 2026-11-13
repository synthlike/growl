# An OAuth client_credentials token request.
POST {{base}}/echo
[BasicAuth]
client: s3cret
[FormParams]
grant_type: client_credentials
scope: read write+admin&x=é
k: 1
k: 2
