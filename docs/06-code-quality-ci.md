# Code quality, CI, and packaging

User-confirmed choices:

| Concern            | Choice                                                                   |
| ------------------ | ------------------------------------------------------------------------ |
| Release packaging  | **GoReleaser** — one tool for cross-compiled binaries + multi-arch Docker image + GitHub Release |
| Container registry | **GHCR only** — `ghcr.io/astromechza/librofm-to-audiobookshelf`           |
| Dep updates        | **Manual** — `go get -u ./... && go mod tidy` on demand, no bot          |

## One tool, many analyzers: golangci-lint

`golangci-lint` is **not a wrapper that shells out to separate
binaries** — it vendors the analyzers themselves and runs them all
in-process against a single parse of each file. Installing
`golangci-lint` gives us, in one binary:

| Bundled inside golangci-lint | Role             |
| ---------------------------- | ---------------- |
| `gofmt`, `goimports`, `gofumpt` | formatting     |
| `govet`, `staticcheck`       | bug-finders (stdlib + Honnef) |
| `errcheck`, `ineffassign`, `unused` | correctness |
| `gosec`                      | security         |
| `bodyclose`, `noctx`         | HTTP hygiene     |
| `errorlint`, `revive`, `nolintlint` | style       |
| `misspell`, `unconvert`, `prealloc` | other      |

Adding `--fix` makes it rewrite files in place — same as
`gofumpt -w` + `goimports -w` combined. So we don't install or
invoke those tools separately; `make fmt` is one `golangci-lint run --fix`.

Only the single generated file (`internal/abs/gen.go`) is exempt — via
a per-file `issues.exclude-rules` entry, not a directory-wide exclusion.
Everything else under `internal/abs/` (the hand-written wrapper, multipart
upload helper, discovery polling code) IS linted normally.

### What is *not* in golangci-lint (and stays as separate jobs/installs)

| Tool          | Why it's separate                                          |
| ------------- | ---------------------------------------------------------- |
| `govulncheck` | Different mechanism: vulnerability DB lookup, not analyzer |
| `oapi-codegen`| Code generator, not a linter                               |
| `goreleaser`  | Packaging, not a linter                                    |
| `shellcheck`  | Not Go                                                     |
| `redocly`     | Not Go (OpenAPI linting)                                   |

## Enabled linters (golangci-lint)

Config in `.golangci.yml` at the repo root.

| Linter       | Why                                                             |
| ------------ | --------------------------------------------------------------- |
| `govet`      | Catches printf-format mismatches, lock copies, etc. (stdlib)     |
| `staticcheck`| Comprehensive bug-finder; SA-rules are uniquely good            |
| `errcheck`   | Fails on ignored errors — important for HTTP clients            |
| `ineffassign`| Unused assignments                                              |
| `unused`     | Dead code, unused funcs/types/vars                              |
| `gofumpt`    | Stricter-than-gofmt formatting                                  |
| `goimports`  | Import ordering                                                 |
| `gosec`      | Security scanning (file perms, weak crypto, etc.)               |
| `bodyclose`  | Enforces `defer resp.Body.Close()` — HTTP-heavy project         |
| `noctx`      | Refuses `http.NewRequest` without context                       |
| `errorlint`  | Use `errors.Is` / `errors.As`, not `==` / type-assert           |
| `revive`     | Configurable replacement for `golint`                           |
| `nolintlint` | Bans bare `//nolint`; comments must name the rule and a reason  |

Excluded with reason:
- `gochecknoglobals` — too aggressive for a CLI with `var rootCmd = ...`.
- `wsl` / `nlreturn` — whitespace bikeshed.
- `lll` — line-length policed only by `gofumpt` indirectly.
- All linters that fire on generated code (handled by `issues.exclude-dirs`).

**Vulnerability scanning:** `govulncheck` runs in CI separately
from `golangci-lint`; it's not a "lint" in the style sense and
treating its findings as hard failures (rather than style warnings)
is the right default.

## Tests

| Mode              | Command                                                  | Where        |
| ----------------- | -------------------------------------------------------- | ------------ |
| Race detector     | `go test -race ./...`                                    | CI mandatory |
| Coverage          | `go test -race -coverprofile=cover.out ./...`            | CI mandatory; threshold check on `internal/` |
| Bench (optional)  | `go test -run='^$' -bench=. ./...`                       | manual       |
| Live probes       | `scripts/probe-*.sh`                                     | local only — never in CI (would need real credentials) |

Coverage threshold: 70% on `internal/` to start. Bump as the project matures.

## Generated-code drift detection

`go generate ./...` must be idempotent. CI runs it and `git diff --exit-code`
to fail on uncommitted regeneration — catches "I edited the spec
but forgot to regenerate" before merge.

## Dockerfile strategy

Multi-stage:

```
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags='-s -w' -trimpath -o /out/librofm-sync ./cmd/librofm-sync

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/librofm-sync /usr/local/bin/librofm-sync
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/librofm-sync"]
```

Why these choices:
- **`CGO_ENABLED=0`**: pure-Go, statically linked, no glibc surprises in distroless.
- **`distroless/static-debian12:nonroot`**: includes CA root certificates (needed to dial libro.fm and ABS over HTTPS) and a non-root user. No shell, no package manager, ~2 MB base layer.
- **`-trimpath`**: build paths don't leak into binaries — reproducibility, smaller binaries, no `/home/runner/...` in stack traces.
- **`-ldflags='-s -w'`**: strip symbol tables and DWARF info — typical ~30% size reduction.
- **`ARG TARGETOS/TARGETARCH`**: buildx-friendly cross-arch builds.

The same Dockerfile is consumed by both GoReleaser (at release tag) and the CI `docker build` smoke test (on every PR).

## GoReleaser

Single `.goreleaser.yaml` at repo root. Responsibilities:

1. **Cross-compiled binaries** for `linux/amd64`, `linux/arm64`, `linux/arm/v7`, `darwin/amd64`, `darwin/arm64`. Windows skipped (CLI is intended for headless servers).
2. **Multi-arch Docker image** to `ghcr.io/astromechza/librofm-to-audiobookshelf`. Tags: `vX.Y.Z`, `vX.Y`, `vX`, `latest`.
3. **GitHub Release** with checksums file and auto-generated changelog from conventional-commit messages.
4. **SBOM** generation via `syft` (built into recent GoReleaser).
5. **Snapshot mode** (`make snapshot`): runs locally for testing without pushing.

Triggering: only on `git tag vX.Y.Z` push. CI on PRs/main does *not* invoke GoReleaser — it only does the build/lint/test smoke checks.

## GitHub Actions workflows

Two workflows:

### `.github/workflows/ci.yml`

Runs on every PR and push to `main`. Independent jobs (matrix-friendly):

| Job             | What it does                                                              |
| --------------- | ------------------------------------------------------------------------- |
| `shellcheck`    | Lints all `scripts/*.sh` with shellcheck. Always runs (no Go needed).      |
| `openapi-lint`  | Lints `api/audiobookshelf.openapi.yaml` with `redocly lint`. Always runs.  |
| `go-build`      | Skipped if no `go.mod`. Runs `go build ./...` for sanity.                  |
| `go-test`       | Skipped if no `go.mod`. Runs `go test -race -coverprofile=...` + threshold.|
| `go-lint`       | Skipped if no `go.mod`. Runs `golangci-lint run`.                          |
| `go-vuln`       | Skipped if no `go.mod`. Runs `govulncheck ./...`.                          |
| `go-generate`   | Skipped if no `go.mod`. Runs `go generate ./...` then `git diff --exit-code`. |
| `docker-build`  | Skipped if no `go.mod`. Runs `docker buildx build --platform linux/amd64,linux/arm64` (no push). |

Skipping uses `if: hashFiles('go.mod') != ''` so the workflow gives value immediately (shellcheck + OpenAPI lint) and grows useful as Go code lands.

### `.github/workflows/release.yml`

Triggered by `v*.*.*` tag push. Single job:

1. Checkout with full history (GoReleaser needs it for changelog).
2. Setup Go.
3. Login to GHCR using `GITHUB_TOKEN` — no extra secret needed (`packages: write` permission).
4. Run `goreleaser release --clean`.

Required repository settings (one-time, manual):
- *Settings → Actions → General → Workflow permissions* → **Read and write**.
- After first release: *Packages → librofm-to-audiobookshelf → Settings → Manage Actions access* → grant write to this repo.

## Makefile targets

Convenience for local dev — every CI step has a `make` equivalent:

| Target           | Action                                                            |
| ---------------- | ----------------------------------------------------------------- |
| `make tools`     | `go install` the four dev tools (golangci-lint, govulncheck, oapi-codegen, goreleaser) |
| `make generate`  | `go generate ./...`                                               |
| `make fmt`       | `golangci-lint run --fix` — rewrites formatting + imports in place |
| `make lint`      | `golangci-lint run` — read-only; fails on style or correctness     |
| `make test`      | `go test -race -coverprofile=cover.out ./...`                     |
| `make vuln`      | `govulncheck ./...`                                               |
| `make build`     | `go build -trimpath -ldflags '-s -w' -o bin/librofm-sync ./cmd/librofm-sync` |
| `make docker`    | `docker buildx build --platform linux/amd64,linux/arm64 .`         |
| `make snapshot`  | `goreleaser release --snapshot --clean`                           |
| `make ci`        | `generate-check lint test vuln` — pre-push gate                    |

Note: `make lint` already catches formatting drift because `gofumpt`
and `goimports` are inside golangci-lint. No separate `fmt-check`
target is needed.

## Pre-commit hooks

Out of scope — too easy to bypass and CI catches everything they would.

## Open question

**Does the user want signed releases (cosign + provenance)?**
Default is "no" for v1; it adds complexity (key management or
keyless OIDC) and most self-hosters won't verify. Easy to add later
by uncommenting the GoReleaser `signs:` and `sboms:` stanzas.
