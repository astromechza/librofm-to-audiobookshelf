#!/usr/bin/env bash
# Probe: end-to-end ABS upload → discover → PATCH media → set cover.
#
# THIS IS INVASIVE: it creates a real library item called
# "librofm-sync probe" in your selected library. You must manually
# delete it afterward.
#
# Confirms the full ingest pipeline works against your ABS instance
# before we write any Go.
set -euo pipefail
source "$(dirname "$0")/_lib.sh"

need_env ABS_URL
need_env ABS_API_TOKEN
need_cmd curl
need_cmd jq

TEST_TITLE='librofm-sync probe'
TEST_AUTHOR='Test Author'
TEST_SERIES='Test Series'
TEST_FILE=/tmp/librofm-probe.mp3

if [[ ! -s "$TEST_FILE" ]]; then
  if command -v ffmpeg >/dev/null 2>&1; then
    echo "generating 1-second silent MP3 at $TEST_FILE"
    ffmpeg -loglevel error -y -f lavfi -i 'anullsrc=r=44100:cl=mono' -t 1 -acodec libmp3lame "$TEST_FILE"
  else
    die "no $TEST_FILE and no ffmpeg available. Drop any small MP3 at $TEST_FILE first."
  fi
fi

# --- 1. Pick library + folder ---
libs=$(curl --fail-with-body --silent --show-error \
  "$ABS_URL/api/libraries" \
  -H "Authorization: Bearer $ABS_API_TOKEN")
lib_id="${ABS_LIBRARY_ID:-$(jq -r 'first(.libraries[] | select(.mediaType == "book")) | .id' <<<"$libs")}"
folder_id=$(jq -r --arg id "$lib_id" '.libraries[] | select(.id == $id) | .folders[0].id' <<<"$libs")
echo "library:  $lib_id"
echo "folder:   $folder_id"

# --- 2. Upload ---
log_req POST "$ABS_URL/api/upload"
curl --fail-with-body --silent --show-error \
  "$ABS_URL/api/upload" \
  -H "Authorization: Bearer $ABS_API_TOKEN" \
  -F "libraryId=$lib_id" \
  -F "folderId=$folder_id" \
  -F "title=$TEST_TITLE" \
  -F "author=$TEST_AUTHOR" \
  -F "series=$TEST_SERIES" \
  -F "files=@$TEST_FILE" \
  -o /dev/null --write-out 'upload status: %{http_code}\n'

# --- 3. Discover the new item (poll, scanner may take a few seconds) ---
echo "polling for new item..."
item_id=""
for delay in 1 2 4 8 16; do
  sleep "$delay"
  results=$(curl --fail-with-body --silent --show-error \
    "$ABS_URL/api/libraries/$lib_id/search?q=$(jq -rn --arg t "$TEST_TITLE" '$t | @uri')&limit=12" \
    -H "Authorization: Bearer $ABS_API_TOKEN")
  item_id=$(jq -r --arg t "$TEST_TITLE" '.book[]?.libraryItem | select(.media.metadata.title == $t) | .id' <<<"$results" | head -1)
  if [[ -n "$item_id" ]]; then
    echo "found after ${delay}s: $item_id"
    break
  fi
  echo "  not yet (waited ${delay}s)"
done
[[ -n "$item_id" ]] || die "scanner never picked up the upload — increase polling or check ABS scanner config"

# --- 4. PATCH /media ---
log_req PATCH "$ABS_URL/api/items/$item_id/media"
patch_body=$(jq -n \
  --arg title "$TEST_TITLE" \
  --arg author "$TEST_AUTHOR" \
  --arg series "$TEST_SERIES" \
  '{
    metadata: {
      title: $title,
      subtitle: "probe subtitle",
      authors: [{name: $author}],
      narrators: ["Test Narrator"],
      publishedYear: "2024",
      publisher: "Probe Publisher",
      description: "Created by probe-abs-upload.sh — safe to delete.",
      isbn: "9780000000001",
      asin: null,
      series: [{name: $series, sequence: "1"}],
      language: "en",
      explicit: false,
      genres: ["Probe"]
    }
  }')
curl --fail-with-body --silent --show-error \
  -X PATCH "$ABS_URL/api/items/$item_id/media" \
  -H "Authorization: Bearer $ABS_API_TOKEN" \
  -H 'Content-Type: application/json' \
  --data "$patch_body" | jq '{updated, title: .libraryItem.media.metadata.title, isbn: .libraryItem.media.metadata.isbn}'

# --- 5. Cover from URL ---
log_req POST "$ABS_URL/api/items/$item_id/cover"
cover_url='https://placehold.co/600x800/png'
curl --fail-with-body --silent --show-error \
  -X POST "$ABS_URL/api/items/$item_id/cover" \
  -H "Authorization: Bearer $ABS_API_TOKEN" \
  -H 'Content-Type: application/json' \
  --data "$(jq -n --arg url "$cover_url" '{url: $url}')" | jq .

echo
echo "DONE."
echo "item id: $item_id"
echo "Title:   $TEST_TITLE"
echo "Please delete this item from ABS manually."
