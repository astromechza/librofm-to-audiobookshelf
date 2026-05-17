# syntax=docker/dockerfile:1.7
#
# Build-from-source Dockerfile, used by:
#   - the CI docker-smoke job (build the whole thing from scratch on every PR)
#   - local dev:  `make docker`
#
# Release-time image builds use Dockerfile.release instead — it embeds the
# pre-built binary GoReleaser produced on the host, so the published image
# and the GitHub Release archive are byte-identical.
#
# Final image is distroless/static + nonroot. Includes CA certs so we can
# dial libro.fm and audiobookshelf over HTTPS. ~10 MB total.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
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
