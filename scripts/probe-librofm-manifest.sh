#!/usr/bin/env bash
# Probe: libro.fm download manifests for $PROBE_ISBN.
#
# Tries packaged_m4b first; on 404 falls back to download-manifest (MP3 zips).
# Does NOT download any audio. Prints what's available and presigned URL hosts.
set -euo pipefail
source "$(dirname "$0")/_lib.sh"

need_env PROBE_ISBN
need_cmd curl
need_cmd jq
[[ -s "$LIBROFM_TOKEN_FILE" ]] || die "no token at $LIBROFM_TOKEN_FILE — run probe-librofm-login.sh first"
TOKEN=$(<"$LIBROFM_TOKEN_FILE")

# --- 1. Book details (sanity) ---
log_req GET "https://libro.fm/api/v10/explore/audiobook_details/$PROBE_ISBN"
details=$(curl --fail-with-body --silent --show-error \
  "https://libro.fm/api/v10/explore/audiobook_details/$PROBE_ISBN" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'User-Agent: okhttp/3.14.9' \
  -H 'Content-Type: application/json')
echo "TITLE:    $(jq -r '.data.audiobook.title' <<<"$details")"
echo "AUTHORS:  $(jq -r '.data.audiobook.authors | join(", ")' <<<"$details")"
echo "DURATION: $(jq -r '.data.audiobook.audiobook_info.duration' <<<"$details") seconds"
echo "TRACKS:   $(jq -r '.data.audiobook.audiobook_info.track_count' <<<"$details")"
echo "COVER:    $(jq -r '.data.audiobook.cover_url' <<<"$details")"
echo

# --- 2. M4B endpoint ---
log_req GET "https://libro.fm/api/v10/audiobooks/$PROBE_ISBN/packaged_m4b"
m4b_status=$(curl --silent --output /tmp/m4b.json --write-out '%{http_code}' \
  "https://libro.fm/api/v10/audiobooks/$PROBE_ISBN/packaged_m4b" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'User-Agent: okhttp/3.14.9' \
  -H 'Content-Type: application/json' || true)

if [[ "$m4b_status" == "200" ]]; then
  m4b_url=$(jq -r '.m4b_url' /tmp/m4b.json)
  echo "M4B AVAILABLE"
  echo "  url host: $(echo "$m4b_url" | sed -E 's#^https?://([^/]+)/.*#\1#')"
  filename=$(printf '%s' "$m4b_url" | grep -oE 'filename=[^&]*' | head -1 | sed 's/^filename=//' | sed 's/+/ /g' | sed 's/%20/ /g' | tr -d '"%')
  echo "  filename: $filename"
else
  echo "M4B NOT AVAILABLE (status $m4b_status), falling back to MP3"
fi
echo

# --- 3. MP3 manifest ---
log_req GET "https://libro.fm/api/v10/download-manifest?isbn=$PROBE_ISBN"
manifest=$(curl --fail-with-body --silent --show-error \
  "https://libro.fm/api/v10/download-manifest?isbn=$PROBE_ISBN" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'User-Agent: okhttp/3.14.9' \
  -H 'Content-Type: application/json')

parts=$(jq -r '.parts | length' <<<"$manifest")
tracks=$(jq -r '.tracks | length' <<<"$manifest")
size_mb=$(jq -r '[.parts[].size_bytes] | add / 1024 / 1024 | floor' <<<"$manifest")
echo "MP3 PARTS:  $parts (~${size_mb} MiB total)"
echo "MP3 TRACKS: $tracks"
jq -r '.parts[] | "  part url host: \(.url | sub("^https?://"; "") | sub("/.*$"; "")), size=\(.size_bytes)"' <<<"$manifest"
