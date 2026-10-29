# syntax=docker/dockerfile:1
# Production image (issue #75). The build stages run on the build machine's
# platform and Go cross-compiles, so the arm64 image needs no QEMU. The runtime
# stage has no RUN step for the same reason.

FROM --platform=$BUILDPLATFORM node:22-bookworm AS web
WORKDIR /src
RUN corepack enable && corepack prepare pnpm@12.16.1 --activate
COPY web/package.json web/pnpm-lock.yaml web/
RUN pnpm -C web install --frozen-lockfile
COPY web web
# vite writes the bundle to internal/web/dist (see web/vite.config.ts).
RUN pnpm -C web build

FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY --from=web /src/internal/web/dist internal/web/dist
# go:embed needs the placeholder; dist is not in the build context
# (.dockerignore), so the committed one is absent here.
RUN touch internal/web/dist/.gitkeep \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
        -ldflags "-s -w -X main.version=$VERSION" -o /out/panel ./cmd/panel

FROM docker:29-cli
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.source="https://github.com/daniel-vuky/server-control-panel" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.version="$VERSION" \
      io.github.daniel-vuky.server-control-panel.image="panel"
ENV PANEL_DATA_DIR=/data
COPY --from=build /out/panel /usr/local/bin/panel
HEALTHCHECK --interval=30s --timeout=3s --start-period=20s --start-interval=2s --retries=3 \
    CMD wget -qO- "http://${PANEL_ADDR:-127.0.0.1:8471}/api/health" >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/panel"]
