# Threat Model (Release 1)

## Assets

- object data at rest
- metadata database state
- credentials and signature material
- audit logs and operational logs

## Trust Boundaries

- client to API boundary (untrusted input)
- API to local filesystem
- API to metadata backend
- operational/admin configuration boundary

## Primary Threats

- credential abuse and brute-force request attempts
- request tampering / signature replay
- accidental secret leakage in logs
- unauthorized delete/destructive operations
- downgrade/misconfiguration of TLS

## Controls (Release 1)

- SigV4 verification + skew validation
- auth failure abuse blocking
- TLS config enforcement with secure minimum version
- structured audit events for auth failures/destructive ops
- secret redaction in panic/log pathways

## Out of Scope (Release 1)

- distributed key management and HSM integration
- object lock legal-hold semantics
- multi-tenant isolation guarantees
