#!/bin/sh
# growl -signer example: hybrid AES + RSA encryption of request and response.
#
#   SERVER_PUBLIC_KEY=server.pub growl -signer examples/signers/aes-rsa.sh api.hurl
#
# Request: the body is encrypted with a fresh AES-256-CBC key and IV and
# sent base64-encoded; the key goes in X-Encrypted-Key, wrapped with the
# server's RSA public key (OAEP, SHA-256), and the IV in X-IV.
# Response: when it carries X-IV, its base64 body is decrypted with the same
# AES key, so growl prints and checks the plaintext.
#
# The key travels from the request phase to the response phase in "state",
# and is listed in "secrets" so growl never prints it. Schemes differ (GCM,
# key per response, where the IV goes, an added signature): adapt to match.
# Needs jq, openssl and xxd.
set -eu
in=$(cat)
q() { printf '%s' "$in" | jq "$@"; }

body() { # $1: request or response; the body exactly, as bytes
	if [ "$(q --arg m "$1" '.[$m] | has("body_base64")')" = true ]; then
		q -r --arg m "$1" '.[$m].body_base64' | openssl base64 -d -A
	else
		q -j --arg m "$1" '.[$m].body // ""'
	fi
}

case $(q -r .phase) in
request)
	: "${SERVER_PUBLIC_KEY:?set SERVER_PUBLIC_KEY to the server PEM public key file}"
	key=$(openssl rand -hex 32)
	iv=$(openssl rand -hex 16)
	enc=$(body request | openssl enc -aes-256-cbc -K "$key" -iv "$iv" | openssl base64 -A)
	wrapped=$(printf '%s' "$key" | xxd -r -p |
		openssl pkeyutl -encrypt -pubin -inkey "$SERVER_PUBLIC_KEY" \
			-pkeyopt rsa_padding_mode:oaep -pkeyopt rsa_oaep_md:sha256 | openssl base64 -A)
	iv64=$(printf '%s' "$iv" | xxd -r -p | openssl base64 -A)
	q --arg enc "$enc" --arg wk "$wrapped" --arg iv "$iv64" --arg key "$key" '{
		request: {
			headers: ([.request.headers[] | select(.[0] | ascii_downcase != "content-type")]
				+ [["Content-Type", "text/plain"], ["X-Encrypted-Key", $wk], ["X-IV", $iv]]),
			body: $enc
		},
		state: {key: $key},
		secrets: [$key]
	}'
	;;
response)
	iv64=$(q -r '[.response.headers[] | select(.[0] | ascii_downcase == "x-iv") | .[1]][0] // ""')
	if [ -z "$iv64" ]; then
		echo '{}' # not encrypted: leave it as it is
		exit 0
	fi
	key=$(q -r .state.key)
	iv=$(printf '%s' "$iv64" | openssl base64 -d -A | xxd -p | tr -d '\n')
	plain=$(body response | openssl base64 -d -A | openssl enc -d -aes-256-cbc -K "$key" -iv "$iv" | openssl base64 -A)
	q --arg plain "$plain" '{response: {
		headers: [.response.headers[] | select(.[0] | ascii_downcase | IN("x-iv", "content-type", "content-length") | not)]
			+ [["Content-Type", "application/json"]],
		body_base64: $plain
	}}'
	;;
esac
