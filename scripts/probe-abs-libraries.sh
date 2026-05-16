#!/usr/bin/env bash
# Probe: list ABS libraries and the items in the first one.
#
# Purpose: confirm where `media.metadata.isbn` actually lives in the
# minified response shape, since that's our join key in ADR-003.
set -euo pipefail
source "$(dirname "$0")/_lib.sh"

need_env ABS_URL
need_env ABS_API_TOKEN
need_cmd curl
need_cmd jq

log_req GET "$ABS_URL/api/libraries"
libs=$(curl --fail-with-body --silent --show-error \
  "$ABS_URL/api/libraries" \
  -H "Authorization: Bearer $ABS_API_TOKEN")

echo "--- libraries ---"
jq -r '.libraries[] | "\(.id)\t\(.name)\t\(.mediaType)\t\(.folders | length) folder(s)"' <<<"$libs"

# Pick the library: env override > first 'book' library > first library
lib_id="${ABS_LIBRARY_ID:-$(jq -r 'first(.libraries[] | select(.mediaType == "book")) // .libraries[0] | .id' <<<"$libs")}"
lib_name=$(jq -r --arg id "$lib_id" '.libraries[] | select(.id == $id) | .name' <<<"$libs")
echo
echo "using library: $lib_name ($lib_id)"
echo

log_req GET "$ABS_URL/api/libraries/$lib_id/items?limit=5&minified=1"
items=$(curl --fail-with-body --silent --show-error \
  "$ABS_URL/api/libraries/$lib_id/items?limit=5&minified=1" \
  -H "Authorization: Bearer $ABS_API_TOKEN")

total=$(jq -r '.total' <<<"$items")
echo "library has $total items (showing first 5)"
echo
echo "--- minified item shape (first item, top-level keys) ---"
jq '.results[0] | keys' <<<"$items"
echo
echo "--- media.metadata of first item ---"
jq '.results[0].media.metadata' <<<"$items"
echo
echo "--- ISBN locations across first 5 items ---"
jq -r '.results[] | "\(.id)\tisbn=\(.media.metadata.isbn // "<null>")\ttitle=\(.media.metadata.title)"' <<<"$items"
