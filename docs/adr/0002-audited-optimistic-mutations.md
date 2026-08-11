# ADR 0002: Audited optimistic mutations

Status: accepted

## Context

Humans and agents can edit the same reminder, including while the iPhone is offline. Silent overwrites would destroy context and trust.

## Decision

Require `expected_revision` for edits and stable `client_request_id` values for every write. Persist the domain mutation and signed audit event in one transaction. Return HTTP 409 with both states when the revision is stale.

## Consequences

Clients must retain revisions and resolve conflicts explicitly. Retries are safe. Every accepted state transition is attributable and independently chain-verifiable.
