#!/usr/bin/env bash
# Probe: audiobookshelf auth + Authelia bypass.
#
# GET /api/me must return JSON (not Authelia's HTML login page).
# If the response Content-Type is text/html, the Authelia bypass rule
# is misconfigured — see docs/02-audiobookshelf-api.md "Authelia bypass".
set -euo pipefail
source "$(dirname "$0")/_lib.sh"

need_env ABS_URL
need_env ABS_API_TOKEN
need_cmd curl
need_cmd jq

log_req GET "$ABS_URL/api/me"

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
ct=$(curl --silent --output "$tmp" --write-out '%{content_type}\n' \
  "$ABS_URL/api/me" \
  -H @<(abs_auth))

if [[ "$ct" != application/json* ]]; then
  echo "FAIL: expected Content-Type: application/json, got: $ct" >&2
  echo "Body (first 400 chars):" >&2
  head -c 400 "$tmp" >&2; echo >&2
  echo "Hint: Authelia is intercepting /api requests. Add a bypass rule for /api/.* on this domain." >&2
  exit 1
fi

user=$(jq -r '.username' "$tmp")
is_admin=$(jq -r '.isAdmin // false' "$tmp")
acct_type=$(jq -r '.type // "?"' "$tmp")
echo "auth OK"
echo "  user:     $user"
echo "  type:     $acct_type"
echo "  isAdmin:  $is_admin"
echo "  token:    $(redact "$ABS_API_TOKEN")"
