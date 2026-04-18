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

## Reproducibility

Build reproducibility is enforced by:

- `CGO_ENABLED=0`
- `-trimpath`
- linker flags `-s -w -buildid=`
- deterministic archive metadata (`tar --sort=name --owner=0 --group=0 --numeric-owner --mtime=@$SOURCE_DATE_EPOCH`)
