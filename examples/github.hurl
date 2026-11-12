# Enter on a request runs it. export GH_USER=someone to change the user.
GET https://api.github.com/users/{{GH_USER}}
Accept: application/vnd.github+json
HTTP 200
[Asserts]
jsonpath "$.login" == "{{GH_USER}}"
jsonpath "$.id" isInteger
jsonpath "$.created_at" isIsoDate
header "Content-Type" contains "application/json"
duration < 5000

GET https://api.github.com/repos/golang/go/languages
HTTP 200
[Asserts]
jsonpath "$.Go" > 1000000
jsonpath "$.*" count > 3
