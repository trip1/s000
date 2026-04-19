# Embedded HTML Client (Go + htmx)

This document defines the initial page/route scaffolding for Step 17.

## Route Map

Full pages:

- `GET /app/login` - operator login shell.
- `GET /app` - dashboard and route overview.
- `GET /app/buckets` - bucket list shell.
- `GET /app/buckets/:bucket` - bucket detail shell.
- `GET /app/buckets/:bucket/objects` - object browser shell.
- `GET /app/buckets/:bucket/objects/:key` - object detail shell.
- `GET /app/uploads` - multipart upload monitor shell.
- `GET /app/settings` - client/session settings shell.
- `GET /app/audit` - audit events shell.

htmx partials:

- `GET /app/partials/buckets`
- `GET /app/partials/objects`
- `GET /app/partials/object-metadata`
- `GET /app/partials/flash`
- `GET /app/partials/pagination`

Static assets:

- `GET /assets/*`

## Template and Asset Structure

- `internal/server/web/templates/layout.html` - shared layout fragments.
- `internal/server/web/templates/pages.html` - page templates.
- `internal/server/web/partials/partials.html` - htmx fragment templates.
- `internal/server/web/static/app.css` - embedded UI stylesheet.
- `internal/server/web/static/app.js` - htmx CSRF header wiring and upload progress UX.

## Implementation Notes

- Templates and static assets are embedded into the binary via `go:embed` in `internal/server/web_ui.go`.
- htmx is used for async fragment loading and progressive page updates.
- UI session auth is cookie-based and starts at `/app/login`.
- CSRF tokens are session-bound and required on HTML form action endpoints.
- htmx requests provide CSRF via `X-CSRF-Token` header when `HX-Request: true`.
- Progressive enhancement form actions are available for create bucket, upload object, and delete object:
  - `POST /app/actions/create-bucket`
  - `POST /app/actions/upload-object`
  - `POST /app/actions/delete-object`
- Download action is available for object detail pages:
  - `GET /app/actions/download-object?bucket=<bucket>&key=<key>`

## Web Client Configuration

- UI credentials are sourced from server options (`UIAccessKey`, `UISecretKey`) and currently mapped from admin bootstrap credentials.
- Session cookie name: `s000_ui_session`.
- Session TTL: 12 hours.
- Global default theme can be configured via `S000_UI_THEME`.
- Per-session theme override is stored in cookie `s000_ui_theme` from `/app/settings`.

## Security Considerations

- All `/app` routes require valid UI session cookie except `/app/login`.
- CSRF token is generated per session and required for HTML form actions.
- htmx requests send CSRF token in `X-CSRF-Token` header.
- Form actions enforce method constraints (`POST` for mutating operations).

## Accessibility and Responsive Verification

- Async status/error regions use `role="status"`/`role="alert"` with `aria-live`.
- Form controls include explicit labels and `for`/`id` mappings.
- Responsive behavior is defined with mobile breakpoint media queries in `app.css`.

## Operational Limitations (Current)

- UI authentication is in-memory session-only (not distributed/session-store backed).
- Upload progress enhancement uses XMLHttpRequest in-browser; no server-side resumable upload management in UI yet.
- Object listing pagination currently supports forward continuation token navigation only.

## Operator Walkthrough

Recommended walkthrough sequence for operator onboarding:

1. Open `/app/buckets` and create a bucket.
2. Open `/app/buckets/:bucket/objects` and upload one object.
3. Open `/app/buckets/:bucket/objects/:key` and verify metadata + download.
4. Delete the object and verify list refresh in partial views.

Screenshot checkpoints to capture in documentation assets:

- Bucket list page with non-empty table.
- Object browser with at least one common prefix and one object.
- Object detail page metadata and download action.
