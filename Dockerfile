# ---- Build-Stage ----
# Der Go-Compiler läuft nativ auf BUILDPLATFORM und cross-compiliert für
# TARGETOS/TARGETARCH. Das vermeidet langsame QEMU-Emulation im Build.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

# Module zuerst laden, damit der BuildKit-Cache unveränderte Abhängigkeiten
# zwischen Builds wiederverwenden kann.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN mkdir -p /out/appdata

# Statisches Binary für die Zielplattform bauen. Die Go-Caches sind sicher über
# Architekturen hinweg nutzbar, weil GOOS/GOARCH Teil des Cache-Schlüssels sind.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-$(go env GOARCH)} \
    go build -trimpath -ldflags="-s -w" -o /out/forecast ./cmd/server

# ---- Runtime-Stage ----
FROM gcr.io/distroless/static:nonroot
WORKDIR /app

COPY --from=build --chown=65532:65532 /out/forecast /app/forecast
COPY --from=build --chown=65532:65532 /out/appdata /app/appdata

ENV FORECAST_ADDR=:8080 \
    FORECAST_DATA_DIR=/app/appdata

VOLUME ["/app/appdata"]
EXPOSE 8080

USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/app/forecast", "-healthcheck"]
ENTRYPOINT ["/app/forecast"]
