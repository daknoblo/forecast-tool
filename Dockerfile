# ---- Build stage ----
# Pin the builder to the native BUILDPLATFORM so the Go toolchain runs natively
# and cross-compiles for the requested TARGETOS/TARGETARCH. This avoids slow
# QEMU emulation of the whole Go build for non-native arches (e.g. arm64).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

# Download modules first, reusing a shared BuildKit cache mount so repeat builds
# don't re-fetch unchanged dependencies.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Build the static binary, cross-compiled for the target platform. The module
# and Go build caches are mounted so only changed packages get recompiled. Go's
# build cache keys already include GOOS/GOARCH, so a shared cache is safe across
# architectures.
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/forecast ./cmd/server

# ---- Runtime stage ----
FROM alpine:3.21
WORKDIR /app

# su-exec lets the entrypoint drop from root to the unprivileged user after
# fixing volume permissions. ca-certificates for good measure.
RUN apk add --no-cache su-exec ca-certificates \
    && addgroup -S appuser \
    && adduser -S -G appuser -H -h /app appuser

COPY --from=build /out/forecast /app/forecast
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh \
    && mkdir -p /app/appdata \
    && chown -R appuser:appuser /app

ENV FORECAST_ADDR=:8080 \
    FORECAST_DATA_DIR=/app/appdata

VOLUME ["/app/appdata"]
EXPOSE 8080

# Start as root so the entrypoint can adjust volume ownership, then it drops
# privileges to appuser via su-exec before running the app.
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
