# AGENTS

## Verified repo facts
- Module path is `ds9labs.com/s000` (`go.mod:1`).
- Declared Go version is `1.25.0` (`go.mod:3`).
- Baseline server entrypoint is `cmd/s000/main.go`.
- Current handler endpoints are `GET /healthz` and `GET /readyz` (`internal/server/handler.go`).

## Canonical dev commands
- `make lint` runs gofmt checks + `go vet`.
- `make test` runs unit tests (`go test ./...`).
- `make integration` runs integration tests (`go test -tags=integration ./test/integration/...`).
- `make build` and `make bench` are available for compile/benchmark loops.

## CI facts
- GitHub Actions workflow is at `.github/workflows/ci.yml`.
- CI jobs: lint, unit, integration, and cross-build matrix (`linux/darwin/windows` x `amd64/arm64`).

## TDD baseline
- Initial tests live in `internal/auth/*_test.go`, `internal/blob/*_test.go`, `internal/config/config_test.go`, `internal/server/*_test.go`, and `test/integration/health_integration_test.go`.

## Auth bootstrap facts
- Non-health routes are behind SigV4 verification middleware.
- Bootstrap admin credentials with `S000_ADMIN_ACCESS_KEY` and `S000_ADMIN_SECRET_KEY` in local/dev startup.

## Metadata backend facts
- Metadata backend selection is validated at startup via `S000_METADATA_BACKEND`.
- Supported backend values are `sqlite`, `libsql`, `postgresql`, `mariadb`, and `valkey`.
- Relational migration plans are defined for `sqlite`/`libsql`/`postgresql`/`mariadb` in `internal/metadata/migrations.go`.
- Startup opens and pings backend connections using SQL drivers (`sqlite`/`libsql`/`pgx`/`mysql`) and Valkey (`go-redis`).
