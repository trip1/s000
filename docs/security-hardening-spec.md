# Security Hardening Spec (Section 14)

This document defines Release 1 security hardening requirements.

## 1) TLS Enforcement and Secure Defaults

- TLS can be enabled via configuration.
- When TLS is enabled, both cert and key file paths are required.
- Server TLS minimum version defaults to TLS 1.2.
- Supported minimum TLS versions for Release 1: `1.2`, `1.3`.

## 2) Secret Redaction

- Panic and audit/request-adjacent log fields must redact sensitive token forms.
- Redaction must cover common fields including authorization signatures and secret keys.
- Internal error responses must remain generic (no secret leakage).

## 3) Brute-Force / Abuse Protections

- Repeated auth failures are tracked by client identity (principal+IP when available).
- Exceeding threshold within configured window triggers temporary blocking.
- Blocked requests receive `SlowDown` response.

## 4) Audit Events

Audit events are emitted for:

- auth failures and blocked attempts
- destructive operations (delete object, delete bucket, abort multipart)

Each event includes timestamp, action, outcome, request id, principal, bucket/key context, and reason.

## 5) Threat Model + Security Review

Release 1 includes:

- explicit threat model document
- security review checklist document
- tracked verification status for each control
