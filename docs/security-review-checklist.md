# Security Review Checklist (Release 1)

- [x] TLS configuration supports secure default minimum version.
- [x] TLS enablement validates cert/key path requirements.
- [x] Auth failures are rate-limited / abuse-protected.
- [x] Panic and sensitive log fields are redacted.
- [x] Audit events exist for auth failures.
- [x] Audit events exist for destructive operations.
- [x] Threat model documented.
- [ ] Manual penetration test against staging environment.
