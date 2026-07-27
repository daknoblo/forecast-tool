# ---- Build stage ----
# The Go compiler runs natively on BUILDPLATFORM and cross-compiles for
# TARGETOS/TARGETARCH. This avoids slow QEMU emulation during the build.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

# Download modules first so BuildKit can reuse the cache for unchanged
# dependencies between builds.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN mkdir -p /out/appdata

# Build a static binary for the target platform. The Go caches are safe to share
# across architectures because GOOS/GOARCH are part of the cache key.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-$(go env GOARCH)} \
    go build -trimpath -ldflags="-s -w" -o /out/forecast ./cmd/server

# ---- Runtime stage ----
FROM gcr.io/distroless/static:nonroot
WORKDIR /app

COPY --from=build --chown=65532:65532 /out/forecast /app/forecast
COPY --from=build --chown=65532:65532 /out/appdata /appdata

ENV FORECAST_ADDR=:8080 \
    FORECAST_DATA_DIR=/appdata

VOLUME ["/appdata"]
EXPOSE 8080

USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/app/forecast", "-healthcheck"]
ENTRYPOINT ["/app/forecast"]
