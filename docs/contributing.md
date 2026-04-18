# Contributing

## Prerequisites
- Go `1.25.x`
- GNU Make

## Local Setup
1. Clone repository.
2. Set admin bootstrap creds for local auth-protected routes:
   - `export S000_ADMIN_ACCESS_KEY=admin`
   - `export S000_ADMIN_SECRET_KEY=change-me`
3. Run `go test ./...` to confirm baseline.
4. Run `make lint` before pushing changes.

## Standard Dev Commands
- `make build` - compile all packages.
- `make test` - run unit tests.
- `make integration` - run integration tests (`-tags=integration`).
- `make lint` - formatting check + `go vet`.
- `make bench` - run benchmarks.
- `go run ./cmd/s000ctl health-inspect --endpoint http://127.0.0.1:9000` - validate local server health.
- `go run ./cmd/s000ctl help` - view current CLI command surface.

## Validation Order
For regular feature work, run in this order:
1. `make lint`
2. `make test`
3. `make integration` (for API or storage changes)

## HTML Client Development Notes

- UI routes are served from the same binary under `/app/*`.
- Login route: `/app/login` using `S000_ADMIN_ACCESS_KEY` and `S000_ADMIN_SECRET_KEY`.
- htmx fragments are under `/app/partials/*`.
- Static UI assets are served under `/assets/*`.
