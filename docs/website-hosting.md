# Bucket Website Hosting (MVP)

This document describes the current S3-style website hosting MVP in `s000`.

## Scope

Current behavior targets a practical baseline for static site hosting:

- Per-bucket website configuration via S3 API (`PutBucketWebsite`, `GetBucketWebsite`, `DeleteBucketWebsite`).
- Anonymous website reads (`GET`/`HEAD`) on a dedicated website endpoint.
- Index and error document support.
- Bucket-level redirect-all support (`RedirectAllRequestsTo`).
- Virtual-host bucket resolution with path-style fallback for local development.

Out of scope for this MVP:

- Advanced website routing rules.
- Full parity coverage for all AWS website edge cases.
- Native single-page application fallback toggle (can be approximated by setting `error_document` to `index.html`).

## Configuration

- `S000_WEBSITE_ENABLED` (default `false`): enables the dedicated website endpoint server.
- `S000_WEBSITE_ADDR` (default `:9001`): listen address for website traffic.
- `S000_WEBSITE_DOMAIN` (optional): domain suffix for virtual-host website requests (example: `website.local`).

## Local Static Site Walkthrough

1. Start server with website endpoint enabled.

```bash
export S000_ADMIN_ACCESS_KEY=admin
export S000_ADMIN_SECRET_KEY=secret
export S000_WEBSITE_ENABLED=true
export S000_WEBSITE_ADDR=:9001
export S000_WEBSITE_DOMAIN=website.local
go run ./cmd/s000
```

2. Create a bucket and upload a static site.

```bash
aws --endpoint-url http://127.0.0.1:9000 s3 mb s3://site
aws --endpoint-url http://127.0.0.1:9000 s3 cp ./site/ s3://site/ --recursive
```

3. Configure website behavior.

```bash
aws --endpoint-url http://127.0.0.1:9000 s3api put-bucket-website \
  --bucket site \
  --website-configuration '{"IndexDocument":{"Suffix":"index.html"},"ErrorDocument":{"Key":"error.html"}}'
```

4. Browse the site.

- Path-style local fallback: `http://127.0.0.1:9001/site/`
- Virtual-host style: route `site.website.local` to local host and browse `http://site.website.local:9001/`

## Request Resolution Rules

- `/` resolves to `index_document` (default `index.html` when unset).
- `/path/` resolves to `path/index_document`.
- `/path` tries exact key first, then `path/index_document` fallback.
- Missing objects return configured `error_document` with HTTP 404 when present.
- Missing website config or disabled/public guard failure returns HTTP 404.

## Headers

Website responses set:

- `Content-Type` from stored object metadata (`content-type`) when present, otherwise inferred from extension.
- `Content-Length` from stored object size.
- `ETag` from stored object ETag.

## Redirect Behavior

When `RedirectAllRequestsTo` is set, website requests return `301 Moved Permanently` to:

- configured host,
- configured protocol (`http` or `https`, otherwise inferred from request TLS),
- original request path and query string.
