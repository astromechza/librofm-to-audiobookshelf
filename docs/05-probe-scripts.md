# Probe scripts

`scripts/probe-*.sh` exercise every external endpoint we plan to call,
using only `curl` + `jq`. Run these **before** writing Go to confirm
the protocol docs match reality on your specific accounts.

All scripts:
- Read credentials from env vars (never accept on the command line).
- Print the request being made (with secrets redacted).
- Print the response (with secrets redacted).
- Exit `0` on success, non-zero on failure.

## Setup

```bash
# libro.fm credentials
export LIBROFM_USER='you@example.com'
export LIBROFM_PASSWORD='your-libro-fm-password'

# audiobookshelf
export ABS_URL='https://audiobookshelf.example.com'  # no trailing slash
export ABS_API_TOKEN='eyJ...'                        # from ABS UI: Settings → Users → (you) → API Tokens
```

A single ISBN to probe download flows against:

```bash
export PROBE_ISBN='9781234567890'   # pick one from your library
```

## Scripts

| Script                              | What it does                                                                                       |
| ----------------------------------- | -------------------------------------------------------------------------------------------------- |
| `scripts/probe-librofm-login.sh`    | POSTs `/oauth/token`, prints the access token (last 8 chars only). Writes token to `/tmp/librofm.token` for the other scripts. |
| `scripts/probe-librofm-library.sh`  | GETs `/api/v10/library` (paginated). Prints title, ISBN, author per book.                          |
| `scripts/probe-librofm-manifest.sh` | For `$PROBE_ISBN`, tries `/packaged_m4b` and `/download-manifest`. Prints which is available, file sizes. **Does not download bytes.** |
| `scripts/probe-abs-auth.sh`         | GETs `/api/me`. Verifies response is JSON (not Authelia HTML). Confirms `/api/*` bypass rule.      |
| `scripts/probe-abs-libraries.sh`    | Lists libraries, then for the selected one lists first page of items showing how ISBN is exposed. |
| `scripts/probe-abs-upload.sh`       | **The most invasive probe.** Uploads a tiny silent MP3, discovers the new item, PATCHes metadata, sets cover from a URL, then prints the resulting item. *Does not clean up* — you'll need to delete the test book from ABS after. |

## Running everything in order

```bash
bash scripts/probe-librofm-login.sh
bash scripts/probe-librofm-library.sh
bash scripts/probe-librofm-manifest.sh
bash scripts/probe-abs-auth.sh
bash scripts/probe-abs-libraries.sh
# Manual: read probe-abs-upload.sh and decide if you want to run it.
bash scripts/probe-abs-upload.sh
```

If any one fails, the issue is **upstream** — fix the assumption in
`docs/01-libro-fm-protocol.md` or `docs/02-audiobookshelf-api.md`
before changing the script.

## Notes on the upload probe

`probe-abs-upload.sh` needs a tiny test MP3 to upload. The script
generates one with ffmpeg if available:

```bash
ffmpeg -f lavfi -i anullsrc=r=44100:cl=mono -t 1 -acodec libmp3lame /tmp/test.mp3
```

If you don't have ffmpeg, drop any small MP3 at `/tmp/test.mp3` first.

Expected outcome: a new library item appears in ABS with title
"librofm-sync probe", author "Test Author", series "Test Series",
description "Created by probe-abs-upload.sh", and the cover from
`https://placehold.co/600x800/png` (or a hardcoded placeholder URL).

**Always delete this test book from ABS afterward.** The probe
script can't safely do this for you — DELETE in ABS is a heavy
operation we don't want to script-test.

## Future: a probe for ID3 tagging

Phase 3 will add `scripts/probe-id3-tags.sh` which downloads one MP3
zip, tags it with our planned ID3 set, and dumps the tags back with
`exiftool` or `mid3v2` for visual inspection. Not part of the v1 probe set.
