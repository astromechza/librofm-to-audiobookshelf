# libro.fm protocol (reverse-engineered)

> **Important correction to prior assumption:** libro.fm does **not** use
> browser session cookies for the audiobook-fetching flow. The
> Android/desktop apps speak to an OAuth2-style endpoint that issues a
> long-lived bearer token. We mimic the app, not the browser.

All evidence below was extracted from the two open-source clients listed
in [README.md](README.md). Endpoint paths are cross-checked between
`librofm-downloader` (Kotlin, current, uses `v10`) and `libro-client`
(TypeScript, older, uses `v7`/`v9`). Where they disagree, the `v10`
path from `librofm-downloader` is the source of truth — it's actively
maintained and matches what the current Android app emits.

## Base configuration

| Item               | Value                                              |
| ------------------ | -------------------------------------------------- |
| Base URL           | `https://libro.fm`                                 |
| Content-Type       | `application/json` for all requests with a body    |
| User-Agent         | `okhttp/5.3.2` (mimics current Android app)        |
| `X-LibroFm-AppVer` | `7.34.8` (REQUIRED — empty 401 without it)         |
| Auth header        | `Authorization: Bearer <access_token>`             |
| Charset            | UTF-8 throughout                                   |

**App-version gate, May 2026.** libro.fm tightened `/oauth/token` to
return an empty 401 when `X-LibroFm-AppVer` is missing. Both required
values are cross-checked against the
[burntcookie90/librofm-downloader Dockerfile](https://github.com/burntcookie90/librofm-downloader/blob/main/Dockerfile)
(actively-maintained reference client). When libro.fm bumps the
required app version again, either edit the defaults in
`internal/librofm/client.go` or override at runtime via the
`--librofm-header KEY=VALUE` CLI flag (repeatable).

## Endpoints

### 1. Authenticate — `POST /oauth/token`

Request body (JSON):

```json
{
  "grant_type": "password",
  "username": "user@example.com",
  "password": "secret"
}
```

Response (200):

```json
{
  "access_token": "eyJhbGciOi...",
  "token_type": "bearer",
  "created_at": 1730000000
}
```

Notes:
- Tokens appear long-lived (no `expires_in` field observed).
- Treat a 401 on any subsequent request as "token expired, re-auth and retry once".
- Failed login returns HTTP non-2xx with an `error` field in the body.

**Evidence:** `librofm-downloader/server/app/.../libro/LibroAPI.kt:15-18`,
`libro-client/src/APIHandler.ts:13-28`.

### 2. List library — `GET /api/v10/library?page=N`

- `page` is 1-indexed.
- Response includes `total_pages`; iterate until `page >= total_pages`.
- Each entry is a `Book` (schema below).

Response shape:

```json
{
  "page": 1,
  "total_pages": 3,
  "audiobooks": [ /* Book[] */ ],
  "tags": [ "fiction", ... ]
}
```

The TypeScript client uses `/api/v7/library` and still works as of its
last commit, so older `v*` paths appear to be kept alive for backward
compatibility. Use `v10` and fall back to `v9`/`v7` only if a hard 404
appears in the wild.

**Evidence:** `LibroAPI.kt:20-24`, `LibroFmClient.ts:51-81`.

### 3. MP3 download manifest — `GET /api/v10/download-manifest?isbn=<ISBN>`

Response:

```json
{
  "isbn": "9781234567890",
  "parts": [
    { "url": "https://...s3...?...", "size_bytes": 104857600 },
    { "url": "https://...s3...?...", "size_bytes":  98765432 }
  ],
  "tracks": [
    { "number": 1, "length_sec": 1834, "chapter_title": "Chapter 1", "created_at": "...", "updated_at": "..." }
  ],
  "expires_at": "2025-...",
  "version": "1",
  "size_bytes": 203622032
}
```

- Each `parts[].url` is a presigned S3 URL pointing at a **ZIP archive of MP3 chapter files**.
- Download promptly — URLs are signed with short TTL (`expires_at`).
- The presigned URLs do **not** need our Bearer token, but sending it is harmless if the URL is on `libro.fm` itself (see DownloadClient.ts:73-78).
- After download, unzip each part into a per-book directory. Filenames inside the ZIP are positional (e.g. `01_title.mp3`); chapter titles come from the manifest's `tracks[]`, not the filenames.
- `tracks[].chapter_title` may be `null` for some books.

**Evidence:** `LibroAPI.kt:39-43`, `Mp3DownloadMetadata.kt`,
`LibroApiHandler.kt:120-153` (zip-extract loop),
`libro-client/src/types.d.ts:75-91`.

### 4. M4B download — `GET /api/v10/audiobooks/{isbn}/packaged_m4b`

Returns 200 with `{ "m4b_url": "https://...s3.../...?response-content-disposition=attachment%3Bfilename%3D..." }` **if** a packaged M4B exists for that ISBN. Returns 404 otherwise — that's the trigger for our MP3 fallback.

- The filename comes from the `response-content-disposition` query parameter of the URL, e.g. `attachment;filename="Title - Author.m4b"`.
- Plus signs in the filename are spaces (URL-encoding artefact).
- M4B contains the cover, chapter markers, and ID3-equivalent (`©nam`, `©ART`, etc.) metadata pre-embedded by libro.fm. **We should still PATCH ABS with our richer metadata** because libro.fm's embedded set is minimal.

**Evidence:** `LibroAPI.kt:45-49`, `M4bMetadata.kt`, `LibroApiHandler.kt:106-118`.

### 5. Book details — `GET /api/v10/explore/audiobook_details/{isbn}`

Returns the same `Book` schema as `library`, but for any ISBN (even ones not in your library). Useful for re-fetching enriched metadata, including the wishlist sync path.

Response:

```json
{ "data": { "audiobook": { /* Book */ } } }
```

**Evidence:** `LibroAPI.kt:33-37`, `BookDetailsResponse.kt`.

### 6. PDF extras URL — `GET /api/v10/library/{isbn}/pdf_extra_url?filename=<name>`

For books that ship with a PDF accompaniment. Returns `{ "pdf_url": "..." }` — another presigned S3 URL. `filename` comes from `Book.audiobook_info.pdf_extras[].filename`.

**Evidence:** `LibroAPI.kt:26-31`, `PdfExtraResponse.kt`.

### 7. Wishlist (out of scope for v1) — `GET /api/v10/explore/wishlist`, `POST /api/v10/explore/wishlist/{isbn}`

Documented here for completeness; we don't need them for sync.

## `Book` schema (canonical)

This is the union of fields seen across `librofm-downloader/server/models/.../libro/Book.kt`
and `libro-client/src/types.d.ts`. Fields the Kotlin tool ignores are
still emitted by libro.fm and may be useful for metadata enrichment.

```jsonc
{
  "isbn": "9781234567890",           // string, 13 digits
  "title": "The Book Title",
  "subtitle": "A Subtitle",          // nullable
  "authors": ["Jane Doe"],           // string[]
  "cover_url": "https://covers.libro.fm/9781234567890_1080.jpg",
  "publisher": "Random House Audio",
  "publication_date": "2024-05-01T00:00:00Z",  // ISO 8601
  "description": "<p>HTML allowed</p>",
  "genres": [ { "name": "Fiction" } ],
  "series": "Foo Trilogy",           // nullable
  "series_num": 2,                   // nullable, int
  "abridged": false,                 // nullable
  "lead": null,                      // nullable
  "audiobook_info": {
    "narrators": ["John Reader"],
    "duration": 38500,               // seconds
    "size_bytes": 412345678,
    "track_count": 24,
    "parts_count": 2,
    "pdf_extras": [
      { "filename": "extras.pdf", "size_bytes": 1234567 }
    ],
    "audio_language": "en"
  },
  "user_metadata": {                 // present in `/library`, absent in `/audiobook_details`
    "finished": false,
    "track_index": 0,
    "track_seconds": 0,
    "last_touched_at": "...",
    "added_at": "...",
    "hidden": false,
    "tags": []
  }
}
```

Fields confirmed present in the older `v7` `libro-client` are marked here
even when the newer `librofm-downloader` Kotlin model omits them — they
remain useful for our PATCH payload to audiobookshelf.

## Gotchas / pitfalls

1. **Don't re-auth on every run.** Cache the token to a small file
   (e.g. `~/.cache/librofm-sync/token.json`, mode 0600). Re-auth only
   on 401. The user explicitly opted out of a separate state store,
   but a cached token is operational not state — it's safe to lose.
2. **Cover URL is on `covers.libro.fm`, not `libro.fm`** — fetch without the bearer token (DownloadClient.ts:73-78 checks the hostname before adding auth headers).
3. **MP3 zip URLs expire fast.** Fetch the manifest immediately before downloading; don't cache it.
4. **`tracks[]` order may not match ZIP entry order.** Sort by `tracks[].number` and zip entries by filename, then zip them positionally — that's what the Kotlin tool does (App.kt:507-509).
5. **Chapter titles may be null.** Don't refuse to write tags if some are missing.
6. **Some books are M4B-only or MP3-only.** A 404 on `/packaged_m4b` is the trigger for fallback; don't conflate with auth errors.
7. **ISBN format.** Always 13 digits, but always stored as a string (preserves leading zeros). Use as the canonical join key.
8. **Filename safety.** The Kotlin tool sanitizes via `PathTokens.kt`. Plan to do the same — strip path separators, illegal chars (`<>:"/\\|?*`), trim trailing dots/spaces (Windows quirk), enforce max length.
9. **Rate limiting.** Unknown. The Kotlin tool runs with `--parallel-count` capped at 3 and 1-minute delays between phases. Mirror that conservatively.
10. **Personal data in tokens.** The token is bearer-equivalent to your password. Log redacted (`tok=eyJ...`), never raw.
