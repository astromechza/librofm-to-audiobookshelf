# Pinned tool versions — bump in lockstep with .github/workflows/*.yml.
GOLANGCI_LINT_VERSION ?= v1.61.0
GOFUMPT_VERSION       ?= v0.7.0
GOVULNCHECK_VERSION   ?= v1.1.3
OAPI_CODEGEN_VERSION  ?= v2.4.1
GORELEASER_VERSION    ?= v2.3.2

GOBIN ?= $(shell go env GOPATH)/bin

.PHONY: help
help:
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ---- tools ---------------------------------------------------------------

.PHONY: tools
tools: ## install pinned dev tools into $GOBIN
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)
	go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

# ---- generation ----------------------------------------------------------

.PHONY: generate
generate: ## regenerate the ABS client from the OpenAPI spec
	go generate ./...

.PHONY: generate-check
generate-check: generate ## fail if generated code is out of date
	@git diff --exit-code -- internal/abs/gen.go \
	  || (echo "ERROR: generated code is stale; run 'make generate' and commit." && exit 1)

# ---- formatting & linting ------------------------------------------------

.PHONY: fmt
fmt: ## rewrite files with gofumpt + goimports
	$(GOBIN)/gofumpt -w .
	go run golang.org/x/tools/cmd/goimports@latest -w -local github.com/astromechza/librofm-to-audiobookshelf .

.PHONY: fmt-check
fmt-check: ## fail if files need formatting
	@diff=$$($(GOBIN)/gofumpt -d .); \
	 if [ -n "$$diff" ]; then echo "$$diff"; echo "ERROR: run 'make fmt'." && exit 1; fi

.PHONY: lint
lint: ## run golangci-lint
	$(GOBIN)/golangci-lint run ./...

.PHONY: vuln
vuln: ## run govulncheck
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
ci: generate-check fmt-check lint test vuln ## pre-push gate; runs all CI checks locally
