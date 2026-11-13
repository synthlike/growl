#!/bin/sh
# growl -signer example: RSA-SHA256 (PKCS#1 v1.5) request signing.
#
#   RSA_PRIVATE_KEY=client.pem growl -signer examples/signers/rsa-sha256.sh api.hurl
#
# Signs METHOD \n PATH?QUERY \n TIMESTAMP \n hex(sha256(body)) with the
# private key and sends X-Timestamp and X-Signature (base64). For RSA-PSS,
# add -sigopt rsa_padding_mode:pss to the openssl dgst line. APIs differ in
# what they sign: change string_to_sign and the headers to match.
# Needs jq and openssl.
set -eu
in=$(cat)
: "${RSA_PRIVATE_KEY:?set RSA_PRIVATE_KEY to a PEM private key file}"

body() {
	if [ "$(printf '%s' "$in" | jq '.request | has("body_base64")')" = true ]; then
		printf '%s' "$in" | jq -r '.request.body_base64' | openssl base64 -d -A
	else
		printf '%s' "$in" | jq -j '.request.body // ""'
	fi
}

method=$(printf '%s' "$in" | jq -r '.request.method')
path=$(printf '%s' "$in" | jq -r '.request.url | sub("^[a-zA-Z]+://[^/]+"; "") | if . == "" then "/" else . end')
ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
hash=$(body | openssl dgst -sha256 -r | cut -d' ' -f1)
string_to_sign=$(printf '%s\n%s\n%s\n%s' "$method" "$path" "$ts" "$hash")
sig=$(printf '%s' "$string_to_sign" | openssl dgst -sha256 -sign "$RSA_PRIVATE_KEY" | openssl base64 -A)

printf '%s' "$in" | jq --arg ts "$ts" --arg sig "$sig" \
	'{request: {headers: (.request.headers + [["X-Timestamp", $ts], ["X-Signature", $sig]])}}'
