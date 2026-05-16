#!/usr/bin/env bash
# Probe: libro.fm login.
#
# POST /oauth/token with {grant_type, username, password}. On success, writes
# the access token to $LIBROFM_TOKEN_FILE (default /tmp/librofm.token) for
# subsequent probes to consume. Token last-8-chars are printed for visual sanity;
# never the full token.
set -euo pipefail
source "$(dirname "$0")/_lib.sh"

need_env LIBROFM_USER
need_env LIBROFM_PASSWORD
need_cmd curl
need_cmd jq

log_req POST 'https://libro.fm/oauth/token'

resp=$(curl --fail-with-body --silent --show-error \
  -X POST 'https://libro.fm/oauth/token' \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: okhttp/3.14.9' \
  --data @<(jq -n \
    --arg u "$LIBROFM_USER" \
    --arg p "$LIBROFM_PASSWORD" \
    '{grant_type: "password", username: $u, password: $p}'))

token=$(jq -r '.access_token // empty' <<<"$resp")
[[ -n "$token" ]] || { echo "$resp" | jq . >&2; die "no access_token in response"; }

umask 077
mkdir -p "$(dirname "$LIBROFM_TOKEN_FILE")"
printf '%s' "$token" > "$LIBROFM_TOKEN_FILE"

echo "login OK"
echo "token (redacted): $(redact "$token")"
echo "wrote: $LIBROFM_TOKEN_FILE"
