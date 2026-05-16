# Pinned tool versions — bump in lockstep with .github/workflows/*.yml.
#
# Note: gofumpt, goimports, staticcheck, govet, errcheck, gosec, revive,
# misspell etc. are all bundled inside golangci-lint. We install it once
# and that single binary covers formatting AND linting (`--fix` rewrites).
#
# Separately installed:
#   govulncheck   — CVE scanner, not a style/correctness analyzer
#   oapi-codegen  — code generator
#   goreleaser    — release packager
GOLANGCI_LINT_VERSION ?= v2.12.2
GOVULNCHECK_VERSION   ?= v1.3.0
OAPI_CODEGEN_VERSION  ?= v2.4.1
GORELEASER_VERSION    ?= v2.3.2

GOBIN ?= $(shell go env GOPATH)/bin

.PHONY: help
help:
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ---- tools ---------------------------------------------------------------

.PHONY: tools
tools: ## install pinned dev tools into $GOBIN
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)
	go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

# ---- generation ----------------------------------------------------------

.PHONY: generate
generate: ## regenerate the ABS client from the OpenAPI spec
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
docker: ## buildx the multi-arch Docker image (no push)
	docker buildx build --platform linux/amd64,linux/arm64 -t librofm-sync:dev .

# ---- release -------------------------------------------------------------

.PHONY: snapshot
snapshot: ## local GoReleaser dry-run
	$(GOBIN)/goreleaser release --snapshot --clean

# ---- aggregate -----------------------------------------------------------

.PHONY: ci
ci: generate-check lint test vuln ## pre-push gate; runs all CI checks locally

