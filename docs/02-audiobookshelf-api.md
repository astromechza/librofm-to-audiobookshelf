# audiobookshelf API & Authelia bypass

## Authentication

**Mechanism:** a long-lived **API key** sent as `Authorization: Bearer <token>`
on every `/api/*` request. No login dance, no token refresh, no cookies.

### How the user provisions the token

In the ABS web UI: *Settings → Users → (your user) → API Tokens → Create*.
ABS stores it as a JWT with `exp: null`, and the same `passport-jwt` strategy
that accepts session JWTs accepts these — `server/Auth.js` uses
`ignoreExpiration: true` and validates manually via
`tokenManager.jwtAuthCheck` so API keys can be revoked but never auto-expire.
Session JWTs and API keys go through the **same** Bearer header.

`jwtFromRequest` extracts the token from either `Authorization: Bearer ...`
**or** the `?token=` query string. We only ever use the header form.

### How the CLI receives it

| Source         | Mechanism                                |
| -------------- | ---------------------------------------- |
| Env var        | `ABS_API_TOKEN` (preferred, cron-friendly) |
| CLI flag       | `--abs-token` (overrides env, ad-hoc use) |
| Never accepted | stdin prompt, config file, command argv with the token visible to `ps` |

Storage on disk: not by this tool. Users keep the token in
`systemd EnvironmentFile=`, a Docker `--env-file`, a k8s Secret, or
a shell-rc file with 0600 mode — same pattern as `ABS_URL`,
`LIBROFM_USER`, `LIBROFM_PASSWORD`. The CLI never writes it anywhere.

### How the generated ABS client uses it

The OpenAPI spec (`api/audiobookshelf.openapi.yaml`) declares one
top-level security scheme:

```yaml
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
security:
  - bearerAuth: []
```

`oapi-codegen` produces a `ClientWithResponses` plus a
`WithRequestEditorFn` option. We construct it once in
`internal/abs/client.go`:

```go
editor := func(_ context.Context, req *http.Request) error {
    req.Header.Set("Authorization", "Bearer "+absToken)
    return nil
}
client, _ := abs.NewClientWithResponses(absURL, abs.WithRequestEditorFn(editor))
```

The same client is then wrapped with a request/response logger
(`internal/logx`) that scrubs the Authorization header before any
log line is written — `Authorization: Bearer ***last8`. The token
never appears unredacted in logs, errors, or panic traces.

### Authelia bypass (deployment prerequisite)

The user's ABS instance sits behind Authelia OIDC. Authelia *must*
be configured to skip auth for the API routes our CLI calls,
otherwise it'll redirect our bearer requests to the Authelia login
page (302 → HTML) and we get parse errors.

Minimal `configuration.yml` snippet for the user to add:

```yaml
access_control:
  rules:
    # Allow API requests through unauthenticated; ABS validates the Bearer itself.
    - domain: audiobookshelf.example.com
      resources:
        - '^/api/.*$'
        - '^/login$'              # so the JWT-issuing endpoint works if ever needed
        - '^/public/.*$'          # covers served from /public (cover art for clients)
      policy: bypass
    # Everything else still requires Authelia login.
    - domain: audiobookshelf.example.com
      policy: two_factor
```

We don't manage this config from our CLI, but the README will need
to call it out as a deployment prerequisite.

### Verifying auth at startup

The very first action the CLI performs is `GET /api/me`. Two
distinct failures we handle explicitly, not as generic JSON errors:

| Symptom                                  | Diagnosis                                                | Action                                               |
| ---------------------------------------- | -------------------------------------------------------- | ---------------------------------------------------- |
| `Content-Type: text/html`                | Authelia is intercepting — bypass rule missing/wrong     | Exit non-zero with link to the Authelia snippet above |
| `401 Unauthorized` with JSON body        | Bearer is rejected by ABS (revoked / typo / wrong user)  | Exit non-zero with "rotate `ABS_API_TOKEN`"          |
| `200` with `{username, ...}`             | Auth OK                                                  | Continue to reconciliation                           |

The cheapest standalone check is `scripts/probe-abs-auth.sh`, which
does the same assertions outside the Go code so the user can
diagnose Authelia issues without rebuilding.

### Token lifetime, rotation, revocation

ABS API keys don't expire on their own. If the user revokes/rotates
one in the UI, our next `/api/me` returns 401 and the run exits
non-zero with a clear message. There's no silent retry against a
stale credential, and no in-process refresh path — by design, since
there's nothing to refresh.

If a future user needs short-lived session JWTs instead (e.g.
no API-token support in some forked ABS), the same Bearer header
works — only the provisioning step changes (`POST /login` to obtain
a JWT, attach it the same way). Out of scope for v1; documented for
future extension.

## Endpoints we'll call

All paths are relative to the ABS base URL (e.g. `https://audiobookshelf.example.com`).

### Discovery

| Method | Path                                | Purpose                                                |
| ------ | ----------------------------------- | ------------------------------------------------------ |
| GET    | `/api/me`                           | Auth probe + current user                              |
| GET    | `/api/libraries`                    | List libraries; we pick the one matching `--library`   |
| GET    | `/api/libraries/:id/items`          | Paginated items — used to build the "already synced" set |
| GET    | `/api/libraries/:id/search?q=ISBN`  | Optional faster lookup for a specific ISBN              |

`/api/libraries/:id/items` supports `page=`, `limit=`, `minified=1`,
`filter=`, `sort=`, `include=`. For our use case we want every item
with its ISBN, so we'll iterate `page=0..` with `limit=200&minified=1`
until the response is empty. The `minified` shape includes `media.metadata.isbn`,
which is the join key. (`server/controllers/LibraryController.js` lists
the query params; the underlying model exposes ISBN under
`libraryItem.media.metadata.isbn`.)

### Upload flow (per book)

1. **POST `/api/upload`** — multipart form. Fields:

   | Field      | Required | Notes                                                                                  |
   | ---------- | -------- | -------------------------------------------------------------------------------------- |
   | `files`    | yes      | One or more files. We send the M4B (1 file) or the MP3s (N files).                     |
   | `libraryId`| yes      | From `/api/libraries`.                                                                 |
   | `folderId` | yes      | From `library.folders[0].id` typically.                                                |
   | `title`    | yes      | Used for directory naming, **not** persisted to metadata.                              |
   | `author`   | no       | Directory naming only.                                                                 |
   | `series`   | no       | Directory naming only.                                                                 |

   Returns `200` with **no body**. ABS writes the file(s) to
   `<library-root>/<author>/<series>/<title>/...` and the auto-scanner
   creates the library item shortly after.

   **Annoying consequence:** we don't get the new item's ID back.
   We have to discover it ourselves — see step 2.

2. **Poll for the new item** — `GET /api/libraries/:id/search?q=<ISBN>`
   or `GET /api/libraries/:id/items?filter=...`. Retry with backoff
   (the scanner may take 1–10s). Match by the directory path we
   uploaded into, or by title+author if path isn't returned. ISBN
   isn't populated yet at this stage (the upload didn't persist it),
   so search by directory/title.

3. **PATCH `/api/items/:id/media`** — JSON body:

   ```json
   {
     "metadata": {
       "title": "The Book Title",
       "subtitle": "A Subtitle",
       "authors": [{ "name": "Jane Doe" }],
       "narrators": ["John Reader"],
       "publishedYear": "2024",
       "publisher": "Random House Audio",
       "description": "...",
       "isbn": "9781234567890",
       "asin": null,
       "series": [{ "name": "Foo Trilogy", "sequence": "2" }],
       "language": "en",
       "explicit": false,
       "genres": ["Fiction"]
     }
   }
   ```

   Response: `{ "updated": true, "libraryItem": { ... } }`.

   Field shape notes (cross-checked against
   `server/controllers/LibraryItemController.js` and
   `server/models/Book.js`):
   - `authors` and `series` are arrays of objects, **not** strings.
   - `narrators` is an array of plain strings.
   - `publishedYear` is a string, not an int.
   - `series[].sequence` is a string ("2", "1.5", "0.5" etc.).
   - `genres` is an array of plain strings.

4. **POST `/api/items/:id/cover`** — multipart with `cover` file,
   *or* JSON `{ "url": "https://covers.libro.fm/..." }`. Use the URL
   form: lets ABS pull the cover directly from libro.fm's CDN,
   no need to proxy bytes through our process.

5. **(Optional) POST `/api/items/:id/match`** — we are explicitly
   **not** using this in v1 (user's decision). It would re-query an
   external provider (Audible/Google) and risk overwriting the
   metadata we just patched. Skip.

### Per-book end-to-end

```
upload (1) ─► poll for item id (2) ─► PATCH media (3) ─► POST cover (4)
```

If any step fails, the rest abort for that book; the next book proceeds.
On the next cron run we'll see the book is in ABS (step "is it already there?")
but with bad/missing metadata. We need an idempotency strategy — see
[03-architecture-decisions.md](03-architecture-decisions.md) ADR-005.

## Gotchas / pitfalls

1. **Auto-scanner race.** ABS may scan and create the item before we PATCH; or, with `scanner.scheduleScansEnabled` off, may not scan at all. We need explicit polling, not "wait N seconds".
2. **Folder layout matters.** Multi-file MP3 uploads must all land in the *same* directory for ABS to treat them as one book. The upload endpoint enforces this via the `title` field, but be careful if titles are sanitized differently per file.
3. **PATCH replaces, doesn't merge, on some array fields.** E.g. sending `genres: []` removes all genres. Always send a complete payload.
4. **Cover URL must be public.** `covers.libro.fm` is, so we're fine. If we ever need to upload from a local path, switch to multipart.
5. **Empty / missing metadata** in `Book.publication_date` etc. — coerce `publishedYear` from the year part only; nil-safe.
6. **403 on upload** means the API key's user lacks `canUpload`. Fix in ABS UI: *Settings → Users → user → Permissions → Upload Files*.
7. **No transactional rollback.** If PATCH fails after upload, the item exists with bad metadata. Our reconciler should re-PATCH on next run for any item whose ISBN is known to us but whose ABS metadata is incomplete.
