# AI chat and planning configuration

## Chat with your data

On **Goals**, choose a preset question or write your own. The server sends the
question and a compact fiscal-year summary to the configured AI deployment:
targets, capacity, project budgets and aggregated booked/forecast hours.
It does not send the raw data file. Chat is read-only and disabled in private
mode; [monthly AI planning](FEATURES.md#monthly-planning) uses a separate
preview/save flow.

Built-in AI prompts follow the selected application language. Saved custom
prompts are retained verbatim and are not translated when the language changes.

## Foundry with Microsoft Entra ID

Set all four variables in `.env` and apply them with `docker compose up -d`:

| Variable | Purpose |
|----------|---------|
| `AZURE_RESOURCE_ID` | Account resource ID: `/subscriptions/<subscription-id>/resourceGroups/<group>/providers/Microsoft.CognitiveServices/accounts/<account>` |
| `AZURE_TENANT_ID` | Entra tenant ID |
| `AZURE_CLIENT_ID` | Application/client ID |
| `AZURE_CLIENT_SECRET` | Application client secret |

Use the **account resource ID**, not a Foundry project URL. Grant the identity
account/deployment read access and inference permissions for the chosen models.
Only Azure public cloud is supported.

Under **Settings → AI endpoint**, select a discovered chat deployment.
**Reload deployments** refreshes the catalog; otherwise it is cached for five
minutes. The selected deployment auto-saves.

Any non-empty Foundry variable enables identity mode. Incomplete configuration,
discovery failures and authentication errors do **not** fall back to an API key
or a manually configured endpoint. Tokens and discovery catalogs are not saved
in the data file.

## Manual API-key mode

Leave all four Foundry variables unset. Under **Settings → AI endpoint**, set:

| Field | Example |
|-------|---------|
| Endpoint URL | `https://my-resource.openai.azure.com` or `https://my-resource.services.ai.azure.com/openai/v1` |
| Deployment | `model-router` |
| API version | `2024-10-21` (classic API only; omitted for v1) |

Set `FORECAST_AI_API_KEY` in `.env` and enable its Compose environment entry
as described under [Configuration](DEPLOYMENT.md#configuration). The key is
never saved in `data.json`. Both classic and OpenAI v1 endpoint formats are
supported.
