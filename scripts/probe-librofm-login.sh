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

# Capture both status and body. `set +e` so a non-2xx doesn't kill the script
# before we can print the body (libro.fm returns useful JSON like
# `{"error":"invalid_grant","error_description":"Invalid email or password."}`).
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
set +e
status=$(curl --silent --show-error --output "$tmp" --write-out '%{http_code}' \
  -X POST 'https://libro.fm/oauth/token' \
  -H 'Content-Type: application/json' \
  -H 'User-Agent: okhttp/3.14.9' \
  --data @<(jq -n \
    --arg u "$LIBROFM_USER" \
    --arg p "$LIBROFM_PASSWORD" \
    '{grant_type: "password", username: $u, password: $p}'))
rc=$?
set -e

if (( rc != 0 )); then
  echo "FAIL: curl exited $rc" >&2
  [[ -s "$tmp" ]] && head -c 2000 "$tmp" >&2
  exit 1
fi
resp=$(<"$tmp")

if [[ "$status" != "200" ]]; then
  echo "FAIL: HTTP $status" >&2
  # libro.fm replies with OAuth-style JSON on 4xx; surface error_description if
  # present, otherwise dump the raw body. `|| true` so a non-JSON body (which
  # makes jq exit non-zero) doesn't get clobbered by set -e.
  detail=$(echo "$resp" | jq -r '.error_description // .error // empty' 2>/dev/null || true)
  if [[ -n "$detail" ]]; then
    echo "  $detail" >&2
  elif [[ -n "$resp" ]]; then
    echo "  $resp" >&2
  fi
  exit 1
fi

token=$(jq -r '.access_token // empty' <<<"$resp")
[[ -n "$token" ]] || { echo "$resp" | jq . >&2; die "no access_token in response"; }

umask 077
mkdir -p "$(dirname "$LIBROFM_TOKEN_FILE")"
printf '%s' "$token" > "$LIBROFM_TOKEN_FILE"

echo "login OK"
echo "token (redacted): $(redact "$token")"
echo "wrote: $LIBROFM_TOKEN_FILE"
