# Security Policy

## Supported Versions

s000 is currently pre-`v1`. Security fixes are prioritized for the latest commit on the default branch and the most recent tagged release line.

## Reporting a Vulnerability

Please do not disclose vulnerabilities in public issues, discussions, or pull requests.

Use one of these private channels:
1. GitHub Security Advisories (preferred): `Security` tab -> `Report a vulnerability`.
2. If Security Advisories are unavailable in your environment, open a private maintainer contact path and include `[security]` in the subject.

Include as much detail as possible:
- Affected version/commit.
- Impact and attack scenario.
- Reproduction steps or proof-of-concept.
- Any suggested mitigations.

## Response Process

- Initial acknowledgment target: within 72 hours.
- We will triage, assess severity, and communicate next steps.
- Fixes will be prepared privately when needed, then released with a coordinated public advisory.

## Scope Notes

High-priority areas include:
- Authentication and request signing (SigV4 / bearer token handling).
- Credential bootstrap and secret handling.
- Metadata backend connectivity and migration paths.
- HTTP request handling, limits, and denial-of-service controls.

## Hardening References

- `docs/security-hardening-spec.md`
- `docs/threat-model.md`
- `docs/security-review-checklist.md`
