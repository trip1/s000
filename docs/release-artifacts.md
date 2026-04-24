# Release Artifacts

This project ships reproducible release tarballs for:

- Linux (`amd64`, `arm64`)
- FreeBSD (`amd64`, `arm64`)
- macOS (`amd64`, `arm64`)

## Build Locally

```bash
make release-artifacts
```

Artifacts are written to `dist/artifacts/`:

- `s000-<version>-<goos>-<goarch>.tar.gz`
- `checksums.txt`

Defaults:

- `VERSION=dev`
- `TARGET_OSES="linux freebsd darwin"`
- `TARGET_ARCHES="amd64 arm64"`
- `SOURCE_DATE_EPOCH=0`

You can override these values by exporting env vars before running `make release-artifacts`.

## GitHub Releases

CI publishes versioned release assets when a tag matching `v*` is pushed, when a GitHub Release is published, or when the workflow is run manually with a `v*` version input.

For a tag such as `v0.1.0`, the release job uploads:

- `s000-v0.1.0-linux-amd64.tar.gz`
- `s000-v0.1.0-linux-arm64.tar.gz`
- `s000-v0.1.0-freebsd-amd64.tar.gz`
- `s000-v0.1.0-freebsd-arm64.tar.gz`
- `s000-v0.1.0-darwin-amd64.tar.gz`
- `s000-v0.1.0-darwin-arm64.tar.gz`
- `checksums.txt`

Branch and pull request builds still produce CI artifacts, named with `ci-<shortsha>`, but do not publish GitHub Releases.

If a release already exists without assets, run the `ci` workflow manually from GitHub Actions and set `version` to the release tag, for example `v0.1.0`.

## Reproducibility

Build reproducibility is enforced by:

- `CGO_ENABLED=0`
- `-trimpath`
- linker flags `-s -w -buildid=`
- deterministic archive metadata (`tar --sort=name --owner=0 --group=0 --numeric-owner --mtime=@$SOURCE_DATE_EPOCH`)
