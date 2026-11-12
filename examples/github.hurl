# Enter on a request runs it. export GH_USER=someone to change the user.
GET https://api.github.com/users/{{GH_USER}}
Accept: application/vnd.github+json
HTTP 200

GET https://api.github.com/repos/synthlike/growl
HTTP *
