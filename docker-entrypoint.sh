#!/bin/sh
set -e

# Ensure the data directory exists and is writable by the unprivileged user.
# This makes bind mounts and pre-existing named volumes (often root-owned)
# work without manual chown on the host.
DATA_DIR="${FORECAST_DATA_DIR:-/app/appdata}"
mkdir -p "$DATA_DIR"

if [ "$(id -u)" = "0" ]; then
    chown -R appuser:appuser "$DATA_DIR" 2>/dev/null || true
    exec su-exec appuser:appuser /app/forecast "$@"
fi

exec /app/forecast "$@"
