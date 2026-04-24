# Launch Post Pack

Paste-ready launch copy tailored for HN and Reddit.

Canonical repo link: https://github.com/trip1/s000

## Show HN

### Suggested Titles
- Show HN: s000 - Self-hosted S3-compatible object storage in Go
- Show HN: s000 - Single-node S3-compatible object storage with SigV4 + UI

### Post Body (paste-ready)

I built `s000`, a self-hosted S3-compatible object storage service in Go.

I wanted a simple single-node object store for dev/lab/internal use where I could keep using familiar S3 tooling (`aws-cli`, SDKs) without pulling in a full distributed stack.

What it does today:
- S3-compatible bucket/object APIs (including multipart upload basics).
- SigV4 on non-health routes.
- Pluggable metadata backends: `sqlite`, `libsql`, `postgresql`, `mariadb`, `valkey`.
- Embedded admin UI and CLI tooling (`s000ctl`).
- Health/readiness probes and metrics endpoint.

Quick try:
```bash
export S000_ADMIN_ACCESS_KEY=admin
export S000_ADMIN_SECRET_KEY=secret
go run ./cmd/s000
curl -fsS http://127.0.0.1:9000/healthz
```

Repo: https://github.com/trip1/s000

Current status: pre-`v1` (actively hardening compatibility + operations).

If you try it, I would especially value feedback on:
1. API compatibility gaps you hit with your existing tools.
2. Operational ergonomics (backups, migrations, observability).
3. Metadata backend priorities for real deployments.

### First Comment (paste-ready)

Author here, happy to answer architecture and implementation questions.

Known limitations are documented in `docs/limitations.md`, and this is currently pre-`v1`.

If you're evaluating it, I can help with:
- Running against your current `aws-cli` or SDK flow.
- Mapping your environment to one of the metadata backends.
- Debugging auth/signing issues quickly.

## Reddit Packs

Use subreddit-specific copy for best results.

### r/selfhosted

#### Title
Open-sourced s000: self-hosted S3-compatible object storage in Go (single-node)

#### Body (paste-ready)
I just open-sourced `s000`, a self-hosted S3-compatible object storage service written in Go.

Goal: keep a simple single-node deployment model while still supporting familiar S3 tooling (`aws-cli`, SDKs).

Current highlights:
- SigV4 verification on non-health routes.
- Metadata backends: `sqlite`, `libsql`, `postgresql`, `mariadb`, `valkey`.
- Embedded UI (`/app/*`) and CLI (`s000ctl`).
- Health/readiness endpoints and Prometheus metrics.

Quick local start:
```bash
export S000_ADMIN_ACCESS_KEY=admin
export S000_ADMIN_SECRET_KEY=secret
go run ./cmd/s000
```

Repo + docs: https://github.com/trip1/s000

It is pre-`v1`, so I expect rough edges. Blunt feedback is very welcome, especially around compatibility and operator UX.

### r/golang

#### Title
I open-sourced s000: S3-compatible object storage in Go (single-node, SigV4)

#### Body (paste-ready)
I built and open-sourced `s000`: an S3-compatible object storage server in Go.

Design target is a practical single-node deployment for dev/lab/internal use while keeping standard S3 client compatibility.

What is implemented now:
- Bucket/object APIs + multipart basics.
- SigV4 verification middleware on all non-health routes.
- Configurable metadata backend at startup (`sqlite`, `libsql`, `postgresql`, `mariadb`, `valkey`).
- Embedded admin UI and `s000ctl` CLI.

Repo: https://github.com/trip1/s000

I would love feedback from Go folks on API shape, package boundaries, and operational defaults.

### r/programming

#### Title
Open source: s000, a self-hosted S3-compatible object storage service in Go

#### Body (paste-ready)
I open-sourced `s000`, a self-hosted object storage service with an S3-compatible API.

It is aimed at teams that want a straightforward single-node setup (dev/lab/internal) while still using existing S3 tooling.

Current capabilities include bucket/object operations, multipart upload basics, SigV4 auth on non-health routes, multiple metadata backends, and an embedded admin UI + CLI.

Repo: https://github.com/trip1/s000

Status is pre-`v1`. I am looking for technical feedback on compatibility gaps and deployment ergonomics.

## Launch Day Reply Snippets

Use these for fast responses in comments.

- "Yes, non-health routes use SigV4 verification by default; health checks remain open for probes and orchestrators."
- "For first run, `sqlite` is the simplest metadata backend; switch via `S000_METADATA_BACKEND` when needed."
- "This is pre-v1 and single-node focused right now; known gaps are tracked in `docs/limitations.md`."
- "If you share the exact failing `aws-cli` or SDK call, I can usually map it to current compatibility quickly."

## Day-Of Posting Plan

1. Post to HN first with the Show HN template.
2. Publish `r/selfhosted` within 15-30 minutes.
3. Publish `r/golang` after first feedback round so you can answer code-level questions quickly.
4. Keep `r/programming` concise and discussion-driven, not feature-list heavy.
