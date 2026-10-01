# Deployment and configuration

## Quick start (container)

Install Docker with the Compose plugin and save
[docker-compose.yml](../docker-compose.yml) in your deployment directory.
Run the following command from that directory:

```bash
docker compose up -d
```

Open **http://localhost:8080**. Compose pulls
`ghcr.io/daknoblo/forecast-tool:latest` and stores data in a persistent named
volume. No local Go installation or build is required. Keep the service on a
private network or protect it with an authenticated reverse proxy.

## Configuration

| Environment variable | Compose value/default | Purpose |
|----------------------|-----------------------|---------|
| `FORECAST_ADDR` | `:8080` | Listen address inside the container |
| `FORECAST_DATA_DIR` | `/appdata` | Mounted directory containing `data.json` |
| `FORECAST_AI_API_KEY` | Unset | API key for manual AI configuration |
| `FORECAST_API_READ_TOKEN` | Unset | Read-only token for the JSON API |
| `FORECAST_API_WRITE_TOKEN` | Unset | Read/write token for the JSON API |

For optional AI credentials and API tokens, copy [.env.example](../.env.example)
to `.env` and fill in the required values. In the compose file, also uncomment
the corresponding `FORECAST_AI_API_KEY`, `FORECAST_API_READ_TOKEN` and/or
`FORECAST_API_WRITE_TOKEN` environment entries. Foundry's four identity
variables are already passed through.

Apply environment changes with `docker compose up -d` to recreate the service;
a plain restart does not reload its environment. Never commit `.env`.
See [Foundry identity configuration](AI.md#foundry-with-microsoft-entra-id).

## Storage permissions

If startup reports `open /appdata/data.json: permission denied`, check volume
ownership. The application container runs as UID **65532** and needs write
access to `/appdata`. The bundled `init-permissions` service fixes named-volume
ownership before the application starts.

For a bind mount such as `./appdata:/appdata`, either mount that same directory
in `init-permissions` or set its ownership on the host from the stack directory:

```bash
sudo chown -R 65532:65532 ./appdata
```

Back up the data volume before changing deployment or storage configuration.

## Security model

**Run behind a trusted reverse proxy or on a private network.** The HTML UI
has no built-in authentication; API tokens do not protect the UI or its export.

- `/api/v1` requires a read or write bearer token. With neither configured,
  the API is disabled and returns `503`.
- State-changing UI requests have same-origin checks. Security headers
  restrict resource loading, framing and browser capabilities.
- Supply credentials through environment variables, not `data.json`. The AI
  client rejects redirects to avoid forwarding credentials to another host.
- The application image runs as a non-root user. The bundled Compose service
  also uses a read-only root filesystem, drops capabilities and enables
  `no-new-privileges`.

Private mode is a presentation feature, not an authentication boundary.

## HTTP API access

External clients can read data, manage projects and synchronize one hours value
per day/project through `/api/v1`.

| Token variable | Access |
|----------------|--------|
| `FORECAST_API_READ_TOKEN` | GET requests |
| `FORECAST_API_WRITE_TOKEN` | Read and write requests |

Send `Authorization: Bearer <token>` on every request. Setting only the write
token is sufficient for a synchronizer that also reads data.

```bash
curl -H "Authorization: Bearer $FORECAST_API_READ_TOKEN" \
  https://forecast.example.com/api/v1/data

# Upsert hours; hours=0 deletes the entry.
curl -X POST https://forecast.example.com/api/v1/entries/sync \
  -H "Authorization: Bearer $FORECAST_API_WRITE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"entries":[{"date":"2026-07-01","projectId":"<id>","hours":6}]}'
```

See the [API reference](API.md) for endpoints and validation rules,
[Forecast Accuracy](API.md#esxp-forecast-accuracy) for the ESXP metric, and
the [Scout template setup](API.md#scout-automation-example) for ESXP-only
synchronization. The template does not synchronize other vacation portals.

## Logging

Logs go to standard output and `appdata/forecast.log`. The file rotates at
10 MB and keeps up to three backups. AI diagnostics include the endpoint,
deployment, prompt/response sizes, finish reason, token usage and duration,
but never the API key.
