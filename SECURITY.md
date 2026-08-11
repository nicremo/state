# Security policy

## Supported versions

Security fixes are provided for the latest tagged release and the current `main` branch.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub private vulnerability reporting for this repository. Include affected versions, reproduction steps, impact and any suggested mitigation.

You should receive an initial response within five business days. Publication timing is coordinated after a fix is available.

## Deployment responsibilities

State is self-hosted software. Operators are responsible for:

- TLS termination and a durable domain.
- Host patching, firewall policy and access control.
- Protecting bootstrap tokens, harness credentials, age recipients and APNs keys.
- Keeping the server, relay and `statectl` updated.
- Testing encrypted backups and restores.
- Revoking credentials when a device or machine is lost.

Never commit environment files, Apple private keys, access tokens or backup identities.

## Security properties

- Every harness has a separate, revocable bearer credential.
- Pairing codes are one-time and expire.
- Mutations require an expected revision and stable request identifier.
- Audit rows are immutable and connected by a signed hash chain.
- Agent actors cannot archive or delete reminders.
- Relay routes are opaque and APNs tokens are encrypted at rest.
- Reminder payloads are encrypted before reaching the relay.
- App Attest protects relay registration in production.

These properties do not make a compromised personal State server confidential. That server intentionally processes plaintext for MCP and full-text search.
