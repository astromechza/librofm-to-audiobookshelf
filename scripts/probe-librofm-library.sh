#!/usr/bin/env bash
# Probe: paginate libro.fm library.
#
# GET /api/v10/library?page=N. Prints title|isbn|first-author per book,
# plus a final count. Reads token from $LIBROFM_TOKEN_FILE.
set -euo pipefail
source "$(dirname "$0")/_lib.sh"

need_cmd curl
need_cmd jq
[[ -s "$LIBROFM_TOKEN_FILE" ]] || die "no token at $LIBROFM_TOKEN_FILE — run probe-librofm-login.sh first"
TOKEN=$(<"$LIBROFM_TOKEN_FILE")

page=1
total_pages=1
count=0
printf 'TITLE\tISBN\tAUTHOR\n'
while (( page <= total_pages )); do
  log_req GET "https://libro.fm/api/v10/library?page=$page"
  resp=$(curl --fail-with-body --silent --show-error \
    "https://libro.fm/api/v10/library?page=$page" \
    -H "Authorization: Bearer $TOKEN" \
    -H 'User-Agent: okhttp/3.14.9' \
    -H 'Content-Type: application/json')
  total_pages=$(jq -r '.total_pages // 1' <<<"$resp")
  jq -r '.audiobooks[]? | [.title, .isbn, (.authors // [] | join(", "))] | @tsv' <<<"$resp"
  pagecount=$(jq -r '.audiobooks | length' <<<"$resp")
  count=$(( count + pagecount ))
  page=$(( page + 1 ))
done

echo "---"
echo "total books: $count   pages walked: $(( page - 1 ))"
