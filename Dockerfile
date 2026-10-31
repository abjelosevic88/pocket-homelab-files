# syntax=docker/dockerfile:1.7
# ---------- 1. build the web UI ----------
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 2. build the Go binary ----------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/pocket-core ./third_party/pocket-core
RUN cd third_party/pocket-core && sha256sum -c SHA256SUMS
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pocket-files ./cmd/pocket-files

# ---------- 3. runtime ----------
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata curl poppler-utils \
 && addgroup -g 1000 app && adduser -u 1000 -G app -D -h /data app
COPY --from=build /out/pocket-files /usr/local/bin/pocket-files
# rclone copies connected drives (Google Drive, Dropbox, OneDrive, iCloud Drive, any rclone remote).
COPY --from=rclone/rclone:1.74 /usr/local/bin/rclone /usr/local/bin/rclone
ENV DATA_DIR=/data PORT=8080 HOST=0.0.0.0
VOLUME ["/data"]
EXPOSE 8080 8081
USER app
WORKDIR /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD curl -fsS http://localhost:8080/healthz || exit 1
LABEL org.opencontainers.image.title="Pocket Files" \
      org.opencontainers.image.description="Self-hosted files: a home folder per person, server folders mounted for the household, shares between people, a WebDAV drive, SkySend-style links encrypted in the browser (expiry, download limits, passwords) and upload links." \
      org.opencontainers.image.source="https://github.com/abjelosevic88/pocket-homelab-files" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["pocket-files"]
