# ---- Build stage ----
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache dependencies first
COPY go.mod go.sum* ./
RUN go mod download

# Build the static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/forecast ./cmd/server

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
