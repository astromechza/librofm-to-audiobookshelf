# Architecture decisions

Lightweight ADRs. Each captures a choice the user made (or that the
evidence forced), the alternatives, and the consequence we have to
live with.

---

## ADR-001 — Format: M4B preferred, MP3-zip fallback

**Decision:** Try `GET /api/v10/audiobooks/{isbn}/packaged_m4b` first.
On 404 (no M4B available for that ISBN), fall back to
`GET /api/v10/download-manifest?isbn=...` and download MP3 zip parts.

**Why:**
- Single-file ingest into ABS is dramatically simpler than N-file ingest.
- M4B has chapters + cover + minimal metadata embedded by libro.fm already.
- We control the metadata enrichment via PATCH anyway, so embedded richness doesn't matter much.

**Consequence:**
- Two code paths: `format/m4b.go` and `format/mp3.go`. Keep their public surface identical (`Download(ctx, book) ([]byte, []File, error)` returning a slice of file blobs).
- ID3 tagging applies only to the MP3 path (M4B already has atom-style tags from libro.fm; over-writing them is fiddly and unnecessary because PATCH does the real work).
- Rejected: M4B-with-ffmpeg-convert fallback. Avoids ffmpeg dependency; user can convert offline if they care.

---

## ADR-002 — Metadata: upload → PATCH /media → cover URL

**Decision:** For every synced book:
1. Upload via `POST /api/upload` (file naming carries title/author for folder structure only).
2. Discover the new library item ID via search/poll.
3. PATCH `/api/items/:id/media` with the full libro.fm metadata payload.
4. POST `/api/items/:id/cover` with `{ "url": "<cover_url>" }`.

**Why:**
- `/api/upload` discards all metadata fields except for folder naming — confirmed in `MiscController.handleUpload`.
- PATCH `/media` *is* persisted to the Book record (`LibraryItemController.updateMedia` → `media.updateFromRequest(mediaPayload)`).
- libro.fm's cover URL is on a public CDN; passing it to ABS as a URL is cheaper than proxying bytes.
- Explicitly **not** using `/api/items/:id/match` — it would re-query an external metadata provider and could clobber our PATCH.

**Consequence:**
- Need post-upload polling (no item ID in the upload response).
- Must send complete metadata payloads on PATCH (array fields like `genres` are replaced, not merged).
- Partial-failure window: if PATCH fails after upload, item exists with bad metadata. See ADR-005.

---

## ADR-003 — State: query ABS instead of keeping a local DB

**Decision:** No local SQLite or JSON state file. On every run:
1. Fetch the libro.fm library (full pagination, fast — usually 1 request).
2. Fetch the ABS library items (paginated, also fast).
3. Join on ISBN. Anything in libro.fm not in ABS is a download candidate.

**Why (user's choice):**
- Zero state to back up, lose, migrate, or get out of sync with reality.
- ABS *is* the ground truth — if the user manually deletes a book, the sync will re-add it (desired behaviour).

**Consequence:**
- Every run pays the cost of listing the full ABS library. At hundreds of books with `limit=200&minified=1`, that's a handful of requests — acceptable.
- ISBN must be reliably present in ABS for already-synced items. Our own PATCH step (ADR-002) sets it. **First-run concern:** existing ABS items that we *didn't* sync (manually added by the user) may not have ISBNs. We'll miss those in the join and re-upload them as duplicates.
- **Mitigation:** also build a secondary "fuzzy" index of `(title, first_author)` from ABS items, and treat a match there as "already exists, skip" with a warning. Always log the resolution so the user can spot weirdness.
- **One small exception:** the libro.fm bearer token gets cached to `~/.cache/librofm-sync/token.json` (mode 0600). That's operational state, not "what's synced" state — it can be wiped any time at the cost of one re-login.

---

## ADR-004 — Runtime: one-shot CLI

**Decision:** Single binary, single subcommand-free CLI. Runs once, exits.
Scheduling is the user's problem (cron, systemd-timer, k8s CronJob).

**Why (user's choice):**
- Easier to test (no scheduler to mock).
- Easier to reason about (no in-process timers or HTTP triggers).
- Easier to deploy (no daemon = no process supervision).

**Consequence:**
- No internal retry loops across runs — a transient failure means "wait for next cron tick".
- Within a single run, retry transient errors with bounded backoff.
- Exit code semantics matter — 0 on success, non-zero on hard error. Log warnings for per-book skips but don't fail the whole run.

---

## ADR-005 — Idempotency / partial-failure recovery

**Decision:** Each run is a full reconciliation, not just a diff-apply.

The reconciliation algorithm:

```
for each libro.fm book:
    find matching ABS item (by ISBN, then by title+author fuzz)
    if no ABS item:
        DOWNLOAD + UPLOAD + DISCOVER_ID + PATCH_MEDIA + SET_COVER
    elif ABS item exists but metadata is incomplete (missing ISBN, etc.):
        DISCOVER_ID + PATCH_MEDIA + SET_COVER       # re-PATCH, no re-download
    else:
        skip
```

**Why:**
- ADR-003 already says we treat ABS as state. This extends it: ABS is also where we observe partial failures (item exists, metadata bad). Self-healing on next run.
- Avoids re-downloading huge M4B files just because a PATCH failed.

**Consequence:**
- Need a clear definition of "metadata is incomplete". Proposed: `media.metadata.isbn == ""` OR `len(media.metadata.authors) == 0`. Treat that as "needs re-PATCH".
- The full-reconciliation pass runs every cron tick. At typical library sizes (hundreds of books), this is cheap. If users have thousands, we'd revisit.

---

## ADR-006 — Configuration

**Decision:** Flags (via `spf13/cobra` or stdlib `flag`) + env-var fallbacks. No config file in v1.

| Setting           | Flag                 | Env var          | Required |
| ----------------- | -------------------- | ---------------- | -------- |
| libro.fm username | `--librofm-user`     | `LIBROFM_USER`   | yes      |
| libro.fm password | `--librofm-password` | `LIBROFM_PASSWORD` | yes    |
| ABS base URL      | `--abs-url`          | `ABS_URL`        | yes      |
| ABS API token     | `--abs-token`        | `ABS_API_TOKEN`  | yes      |
| ABS library name  | `--abs-library`      | `ABS_LIBRARY`    | yes      |
| Cache dir         | `--cache-dir`        | `CACHE_DIR`      | no (default `~/.cache/librofm-sync`) |
| Work dir          | `--work-dir`         | `WORK_DIR`       | no (default `os.TempDir()/librofm-sync`) |
| Dry run           | `--dry-run`          | `DRY_RUN`        | no       |
| Verbose           | `-v`, `--verbose`    | -                | no       |
| Limit (debug)     | `--limit`            | `LIMIT`          | no       |
| Extra HTTP header | `--extra-header`     | -                | no, repeatable |

**Why:**
- Env vars play well with cron, systemd, Docker, k8s secrets.
- No config-file format to bikeshed in v1.
- An `--extra-header` escape hatch lets the user inject custom headers if libro.fm starts demanding new ones, without us cutting a release.

---

## ADR-007 — Dependencies

Pinned minimal set. Each justified.

| Dep                                                | Used for                                              |
| -------------------------------------------------- | ----------------------------------------------------- |
| `github.com/spf13/cobra`                           | CLI scaffolding (optional — could use stdlib `flag`)  |
| `github.com/bogem/id3v2/v2`                        | ID3v2.4 tag writing for MP3 path                      |
| `github.com/oapi-codegen/oapi-codegen/v2`          | Build-time only — generates the ABS client from `api/audiobookshelf.openapi.yaml`. Pinned via `tools/tools.go`. |
| `github.com/oapi-codegen/runtime`                  | Runtime helpers the generated ABS client links against. |
| stdlib `archive/zip`                               | Extract MP3 zip parts                                 |
| stdlib `net/http`                                  | Both libro.fm (hand-written) and ABS (under the generated client) |
| stdlib `encoding/json`                             | libro.fm wire-format                                  |
| stdlib `mime/multipart`                            | `POST /api/upload` body (oapi-codegen handles JSON; multipart we craft ourselves) |

Avoid: ORMs, framework HTTP clients, third-party logging libraries
(stdlib `log/slog` is fine).

**Confirm with user before adding anything beyond this list.**

## ADR-008 — Logging & secrets

**Decision:** `log/slog` with a JSON handler, level controlled by `-v`.
Always redact: bearer tokens (`Authorization: Bearer ***`), passwords,
the `access_token` field in any logged JSON. Use a custom
`slog.Handler` wrapper or per-call sanitization.

ISBNs, titles, and authors are not secrets — log them freely.

---

## ADR-009 — ABS client is generated from OpenAPI

**Decision:** The audiobookshelf HTTP client is **not** hand-written.
The spec at [`api/audiobookshelf.openapi.yaml`](../api/audiobookshelf.openapi.yaml)
captures the subset of ABS endpoints we depend on; `oapi-codegen` produces
typed models and a client from it via `go generate`.

**Why:**
- The spec doubles as documentation of exactly which ABS surface area we depend on — no incidental coupling.
- Drift-detection becomes trivial: CI runs `go generate ./...` and `git diff --exit-code`; any change to the spec or codegen template fails loudly.
- Adding new endpoints is a spec edit + regen, no client boilerplate to write.
- Typed responses end the "what shape is `media.metadata` again?" question forever.

**Consequence:**
- Multipart endpoints (`/api/upload`, multipart variant of `/api/items/:id/cover`) get a hand-written helper alongside the generated client — OpenAPI multipart codegen in Go is awkward and we'd lose more than we gain.
- The generated file is committed so fresh checkouts don't need `go generate`. CI enforces it stays in sync.
- We do **not** apply the same approach to the libro.fm client (no published spec; pretending otherwise would create false stability).

---

## Open questions

These are flagged for the implementation phase, not blocking the plan:

1. **What is the actual ABS minified shape for `media.metadata.isbn`?** Probe needed (`scripts/probe-abs-libraries.sh`).
2. **How does ABS's auto-scanner interact with our upload?** If it has fired and created the item before we PATCH, are there any race conditions with metadata being overwritten by a scan? Probe needed.
3. **Does `POST /api/items/:id/cover` with `{"url":...}` actually work?** Most ABS instructions show multipart upload; the URL form is implied by some forks. Probe needed before relying on it.
4. **What does libro.fm return for accounts with 2FA enabled?** No evidence either way in the prior-art repos. Document as "untested" and surface 401 errors clearly.
5. **Token lifetime.** No `expires_in` in the response. Treat as "valid until 401", but worth confirming empirically.
