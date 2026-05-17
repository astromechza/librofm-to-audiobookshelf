# syntax=docker/dockerfile:1.7
#
# Embeds the pre-built `librofm-sync` binary (produced by GoReleaser, or by the
# CI snapshot step) into a distroless/static image. No `go build` here — the
# binary in the GitHub Release archive and the binary in the published
# container image are always byte-identical.
#
# Build context must contain a `librofm-sync` binary at its root. GoReleaser
# places it there automatically; the CI smoke job stages it from `dist/`.

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source=https://github.com/astromechza/librofm-to-audiobookshelf
LABEL org.opencontainers.image.description="Sync purchased audiobooks from libro.fm to audiobookshelf"
LABEL org.opencontainers.image.licenses=MIT
COPY librofm-sync /usr/local/bin/librofm-sync
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/librofm-sync"]
