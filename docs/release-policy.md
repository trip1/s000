# Release Policy

## Semantic Versioning
- `0.y.z` before 1.0: breaking changes are allowed between minor versions.
- `1.0.0` is the first production stability contract.
- After `1.0.0`:
  - `MAJOR`: incompatible API or on-disk format changes.
  - `MINOR`: backward-compatible features.
  - `PATCH`: backward-compatible fixes and security updates.

## Release 1 Scope Freeze Criteria
Scope for `v0.1.0` (Release 1) freezes when all are true:
- API scope matches `SPEC.md` Release 1 section.
- No new features accepted without removing an equivalent-size item.
- Only bug fixes, docs, performance tuning, and compatibility fixes are allowed.
- Exit criteria in `SPEC.md` are tracking green in CI.
