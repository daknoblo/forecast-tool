# ---- Build stage ----
FROM golang:1.23-alpine AS build
WORKDIR /src

# Cache dependencies first
COPY go.mod go.sum* ./
RUN go mod download

# Build the static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/forecast ./cmd/server
# Pre-create the data directory so it can be copied with the right ownership
RUN mkdir -p /out/appdata

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/forecast /app/forecast
# Data directory owned by the non-root runtime user (uid/gid 65532)
COPY --from=build --chown=nonroot:nonroot /out/appdata /app/appdata

ENV FORECAST_ADDR=:8080 \
    FORECAST_DATA_DIR=/app/appdata

VOLUME ["/app/appdata"]
EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/app/forecast"]
