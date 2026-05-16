# syntax=docker/dockerfile:1.7
#
# Multi-stage build. Same Dockerfile is used by:
#   - GoReleaser at release time (cross-arch via TARGETOS/TARGETARCH)
#   - CI smoke build on every PR (no push)
#   - Local dev: `make docker`
#
# Final image is distroless/static + nonroot. Includes CA certs so we can
# dial libro.fm and audiobookshelf over HTTPS. ~10 MB total.

FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build
WORKDIR /src

# Cache module downloads in their own layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Build.
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build \
      -trimpath \
      -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.date=${DATE}" \
      -o /out/librofm-sync \
      ./cmd/librofm-sync

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source=https://github.com/astromechza/librofm-to-audiobookshelf
LABEL org.opencontainers.image.description="Sync purchased audiobooks from libro.fm to audiobookshelf"
LABEL org.opencontainers.image.licenses=MIT
COPY --from=build /out/librofm-sync /usr/local/bin/librofm-sync
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/librofm-sync"]
