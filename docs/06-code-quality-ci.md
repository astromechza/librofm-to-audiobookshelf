# Code quality, CI, and packaging

User-confirmed choices:

| Concern            | Choice                                                                   |
| ------------------ | ------------------------------------------------------------------------ |
| Release packaging  | **GoReleaser** — one tool for cross-compiled binaries + multi-arch Docker image + GitHub Release |
| Container registry | **GHCR only** — `ghcr.io/astromechza/librofm-to-audiobookshelf`           |
| Dep updates        | **Manual** — `go get -u ./... && go mod tidy` on demand, no bot          |

## Go version policy

**`go.mod` is the single source of truth.** Both CI workflows and the
release workflow set their toolchain via `go-version-file: go.mod`. There
is no `GO_VERSION` env var to keep in sync.

Current pin: **`go 1.26.0`** (patch-pinned for reproducibility). Bump by
editing `go.mod` only; everything else (CI, release, Dockerfile, linter)
picks it up.

The `.golangci.yml` `run.go` mirrors the same version for documentation;
it doesn't drive toolchain selection.

> Reading from `go.mod` doesn't automatically prevent **install-time**
> toolchain downgrades — see "Tool install vs go.mod Go version" below
> for the `GOTOOLCHAIN=local` requirement.

### Tool install vs `go.mod` Go version — `GOTOOLCHAIN=local`

Both `golangci-lint` and `govulncheck` embed their own `go/types` to load
source. The version of `go/types` baked into the binary is whatever Go
version the binary was built with. That has to be **≥** the `go`
directive in our `go.mod` — or the loader rejects our source as
`package requires newer Go version`.

Subtle trap: even though `actions/setup-go` and our local toolchain run
Go 1.26, **`go install` may downgrade**. Each tool's own `go.mod` says
`go 1.25.0`, which Go's auto-toolchain treats as "1.25 is sufficient,
let me use 1.25 to build it" — producing a 1.25-built binary that can't
parse our 1.26 source.

Fix: install with `GOTOOLCHAIN=local` (force the install to use the Go
version already in `$PATH`):

```bash
GOTOOLCHAIN=local go install golang.org/x/vuln/cmd/govulncheck@v1.3.0
GOTOOLCHAIN=local go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
```

This is wired up in both the CI workflow (per `env:` block on the
install step) and the `Makefile` `tools` target.

`golangci-lint-action@v7` is unaffected — it downloads pre-built release
binaries from GitHub, not `go install`-built ones — so the `lint` job
doesn't need the `GOTOOLCHAIN` trick.

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

Two Dockerfiles, one purpose each:

| File                | Used by                                                | Behaviour                                  |
| ------------------- | ------------------------------------------------------ | ------------------------------------------ |
| `Dockerfile`        | CI docker-smoke job, `make docker`, local dev          | Multi-stage: `golang:1.26-alpine` → builds the binary inside the image → COPYs into a distroless/static + nonroot final stage. |
| `Dockerfile.release`| GoReleaser at release-tag time                         | Single-stage: starts from distroless/static + nonroot, COPYs the binary GoReleaser pre-built on the host. No `go build` here. |

**Why two files.** GoReleaser already cross-compiles the binary on the
host (so the binary in the GitHub Release archive and the binary in the
container image are byte-identical). If the release image also ran
`go build`, we'd have two compilations producing potentially different
binaries — defeating the point. The build-from-source `Dockerfile` is
kept for the CI smoke test and local dev, where we want to verify the
whole pipeline works without needing GoReleaser.

Shared properties of the final image (both paths produce the same shape):

- **`CGO_ENABLED=0`**: pure-Go, statically linked, no glibc surprises in distroless.
- **`distroless/static-debian12:nonroot`**: includes CA root certificates (needed to dial libro.fm and ABS over HTTPS) and a non-root user. No shell, no package manager, ~2 MB base layer.
- **`-trimpath`** + **`-ldflags='-s -w'`**: reproducibility + ~30% size reduction.
- **`ARG TARGETOS/TARGETARCH`** (Dockerfile only): buildx-friendly cross-arch.

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
