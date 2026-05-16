# librofm-to-audiobookshelf — Planning & Investigation

A Go CLI to sync purchased audiobooks from [libro.fm](https://libro.fm) into
a self-hosted [audiobookshelf](https://www.audiobookshelf.org/) instance.

Status: **investigation / planning** — no production code yet.

## Decisions confirmed with user

| Topic           | Choice                                                               |
| --------------- | -------------------------------------------------------------------- |
| Audio format    | M4B (packaged) with MP3-zip fallback                                 |
| Metadata path   | Upload → PATCH `/api/items/:id/media` → POST cover (no ISBN-match)   |
| State store     | Use the audiobookshelf library itself as the source of truth         |
| Runtime         | One-shot CLI; user schedules with cron / systemd-timer / k8s-cronjob |
| Language        | Go                                                                   |

## Contents

| Doc                                                  | What's in it                                                |
| ---------------------------------------------------- | ----------------------------------------------------------- |
| [01-libro-fm-protocol.md](01-libro-fm-protocol.md)   | Reverse-engineered libro.fm API: auth, library, manifests, schemas, gotchas. Evidence cited. |
| [02-audiobookshelf-api.md](02-audiobookshelf-api.md) | ABS endpoints we'll call, auth model, Authelia bypass requirements. |
| [03-architecture-decisions.md](03-architecture-decisions.md) | ADR-style record of the choices above and their consequences. |
| [04-implementation-plan.md](04-implementation-plan.md) | Phased plan, Go module layout, dependencies, open questions. |
| [05-probe-scripts.md](05-probe-scripts.md)           | How to use `scripts/probe-*.sh` to validate every assumption before writing Go. |
| [06-code-quality-ci.md](06-code-quality-ci.md)       | Formatting, linting, static analysis, tests, Docker, GoReleaser, GitHub Actions. |
| [../api/audiobookshelf.openapi.yaml](../api/audiobookshelf.openapi.yaml) | OpenAPI 3.1 spec for the ABS subset we use — source of truth for the generated client (see ADR-009). |
| [../api/oapi-codegen.yaml](../api/oapi-codegen.yaml) | Generator config for `oapi-codegen`. |
| [../.golangci.yml](../.golangci.yml)                 | Active golangci-lint configuration.                                              |
| [../.goreleaser.yaml](../.goreleaser.yaml)           | GoReleaser configuration for binary + Docker releases.                          |
| [../Dockerfile](../Dockerfile)                       | Multi-stage build → distroless/static + nonroot.                                |
| [../Makefile](../Makefile)                           | `make ci` runs the same checks GitHub Actions runs.                              |
| [../.github/workflows/](../.github/workflows/)       | `ci.yml` (PR/main) and `release.yml` (tag).                                     |

## Source material

All protocol details were reverse-engineered from these open-source clients
(cloned to `/tmp/research/` during investigation, not vendored here):

- [`burntcookie90/librofm-downloader`](https://github.com/burntcookie90/librofm-downloader) — Kotlin daemon, uses `/api/v10/*` endpoints, most current
- [`jedwards1230/libro-client`](https://github.com/jedwards1230/libro-client) — TypeScript CLI, uses `/api/v7/library` and `/api/v9/download-manifest` (older but corroborates the auth flow)
- [`advplyr/audiobookshelf`](https://github.com/advplyr/audiobookshelf) — server source, for ABS endpoint shapes

None of these tools combine the two halves. This project is the missing glue.
