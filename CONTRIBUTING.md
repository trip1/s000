# Contributing to s000

Thanks for helping improve s000.

## Development Prerequisites
- Go `1.25.x`
- GNU Make

## Local Setup
1. Clone the repository.
2. Set local bootstrap credentials for auth-protected routes:
   - `export S000_ADMIN_ACCESS_KEY=admin`
   - `export S000_ADMIN_SECRET_KEY=change-me`
3. Run baseline checks:
   - `make lint`
   - `make test`

## Standard Commands
- `make build` - compile all packages.
- `make lint` - gofmt checks + `go vet`.
- `make test` - run unit tests (`go test ./...`).
- `make integration` - run integration tests (`go test -tags=integration ./test/integration/...`).
- `make bench` - run benchmarks.

## Validation Expectations for PRs
- Run `make lint` and `make test` for all changes.
- Run `make integration` for API, storage, metadata, auth, or config behavior changes.
- Keep changes scoped and include tests when behavior changes.

## Pull Request Guidelines
- Explain why the change is needed and how it was validated.
- Link related issues when available.
- Update docs when behavior or configuration changes.

## Commit Style
- Use concise, imperative commit messages (for example: `fix metadata startup timeout handling`).

## Security Issues
- Do not open public issues for vulnerabilities.
- Follow `SECURITY.md` for private reporting instructions.

## More Contributor Docs
- Detailed contributor notes: `docs/contributing.md`.
- Operations and deployment references: `docs/operations-guide.md`, `docs/deployment-examples.md`.
