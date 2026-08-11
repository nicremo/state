# ADR 0003: Plaintext-blind push relay

Status: accepted

## Context

Apple push credentials should not be installed on every personal server. A shared relay must not become a central database of private reminder content or personal APNs tokens.

## Decision

The app registers through App Attest and receives an opaque route. The personal server encrypts notification content to the device using X25519, HKDF and AES-GCM. The relay stores APNs tokens encrypted at rest and forwards only the opaque route plus encrypted envelope.

## Consequences

The notification extension is required to show reminder content from remote pushes. Failure produces generic text. The relay still observes timing and route-level metadata. APNs credentials remain centralized and protected.
