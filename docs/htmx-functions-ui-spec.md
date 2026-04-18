# HTMX Functions UI Spec

This spec defines the embedded HTML/htmx operator UI for WASM Functions.

## Scope

The UI must cover full operator workflows already available in the Functions API:

- list and inspect functions
- create/update/delete functions
- list and activate immutable versions
- invoke functions with ad-hoc payloads
- inspect templates, metrics, alerts, and logs

## Routes

### Pages

- `GET /app/functions`
  - Functions index page.
  - Shows summary cards and a table loaded via htmx partial.

- `GET /app/functions/{name}`
  - Function detail page.
  - Shows active config, version controls, invoke panel, metrics, alerts, logs.

### Partials

- `GET /app/partials/functions`
- `GET /app/partials/function-versions?name={name}`
- `GET /app/partials/function-metrics`
- `GET /app/partials/function-alerts`
- `GET /app/partials/function-logs?limit={n}`

### Actions

- `POST /app/actions/functions/create`
- `POST /app/actions/functions/update`
- `POST /app/actions/functions/delete`
- `POST /app/actions/functions/activate`
- `POST /app/actions/functions/invoke`

All mutating actions require authenticated UI session and valid CSRF token.

## Forms and Fields

### Create/Update

- `name` (required for create)
- `runtime` (`wazero`/`wasmer`)
- `trigger`
- `priority`
- `enabled` (checkbox)
- `module_base64` (required on create, optional on update)

### Activate

- `name`
- `version`

### Invoke

- `name`
- `payload` (JSON string)

## UX Guarantees

- Operator should be able to complete all function lifecycle operations without using external API clients.
- Forms show flash/error outcomes on action completion.
- Detail page reflects latest active version after activation.
- Partial endpoints support htmx polling/refresh for metrics/alerts/logs.

## Non-Goals (Current Iteration)

- File upload for wasm modules (base64 text input is accepted for now).
- Rich editor/IDE experience.
- Streaming logs over SSE/WebSocket.
