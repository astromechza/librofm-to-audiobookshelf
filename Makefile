# Pinned tool versions — bump in lockstep with .github/workflows/*.yml.
#
# Note: gofumpt, goimports, staticcheck, govet, errcheck, gosec, revive,
# misspell etc. are all bundled inside golangci-lint. We install it once
# and that single binary covers formatting AND linting (`--fix` rewrites).
#
# `oapi-codegen` is NOT installed here — it's declared via the `tool`
# directive in go.mod (Go 1.24+ feature). `go generate` resolves it
# automatically through `go tool oapi-codegen`.
#
# Separately installed:
#   golangci-lint — see note above
#   govulncheck   — CVE scanner, not a style/correctness analyzer
#   goreleaser    — release packager
GOLANGCI_LINT_VERSION ?= v2.12.2
GOVULNCHECK_VERSION   ?= v1.3.0
GORELEASER_VERSION    ?= v2.3.2

GOBIN ?= $(shell go env GOPATH)/bin

.PHONY: help
help:
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ---- tools ---------------------------------------------------------------

.PHONY: tools
tools: ## install pinned dev tools into $GOBIN
	# GOTOOLCHAIN=local forces installs to use whatever Go is on PATH (must
	# be >= the version in go.mod). Without it, Go's auto-toolchain reads
	# each tool's own `go` directive and may downgrade — producing a binary
	# whose embedded go/types can't parse our source. Same trick is in
	# .github/workflows/ci.yml.
	GOTOOLCHAIN=local go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOTOOLCHAIN=local go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	GOTOOLCHAIN=local go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

# ---- generation ----------------------------------------------------------

.PHONY: generate
generate: ## regenerate the ABS client from the OpenAPI spec (uses `go tool`)
	go generate ./...

.PHONY: generate-check
generate-check: generate ## fail if generated code (or go.mod/go.sum) is out of date
	@go mod tidy
	@git diff --exit-code \
	  || (echo "ERROR: generated files / go.mod / go.sum are stale; run 'make generate && go mod tidy' and commit." && exit 1)

# ---- formatting & linting ------------------------------------------------
#
# golangci-lint bundles gofumpt, goimports, staticcheck, govet, errcheck,
# gosec, revive, misspell etc. `--fix` rewrites formatting/imports;
# without it, the linter just reports.

.PHONY: fmt
fmt: ## rewrite files via golangci-lint --fix (gofumpt + goimports + others)
	$(GOBIN)/golangci-lint run --fix ./...

.PHONY: lint
lint: ## run all golangci-lint checks (no rewriting; fails on style or correctness)
	$(GOBIN)/golangci-lint run ./...

.PHONY: vuln
vuln: ## run govulncheck (separate tool — CVE scanning, not lint)
	$(GOBIN)/govulncheck ./...

# ---- tests & build -------------------------------------------------------

.PHONY: test
test: ## go test with race detector + coverage
	go test -race -coverprofile=cover.out -covermode=atomic ./...

.PHONY: build
build: ## build the CLI binary into bin/
	mkdir -p bin
	go build -trimpath -ldflags='-s -w' -o bin/librofm-sync ./cmd/librofm-sync

.PHONY: docker
docker: ## build the Docker image via goreleaser snapshot (single host platform)
	## Mirrors the CI smoke job: produces the same Dockerfile-driven image
	## the release tag will publish, without needing to push or tag.
	$(GOBIN)/goreleaser build --snapshot --single-target --clean
	@binary=$$(find dist -type f -name librofm-sync | head -1); \
	  cp "$$binary" librofm-sync; \
	  docker build -f Dockerfile -t librofm-sync:dev . ; \
	  rm -f librofm-sync

# ---- release -------------------------------------------------------------

.PHONY: snapshot
snapshot: ## local GoReleaser dry-run
	$(GOBIN)/goreleaser release --snapshot --clean

# ---- aggregate -----------------------------------------------------------

.PHONY: ci
ci: generate-check lint test vuln ## pre-push gate; runs all CI checks locally
