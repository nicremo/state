# ADR 0004: Offline-first iOS sync

Status: accepted

## Context

Reminder viewing and editing must work without network access. A due notification must survive a personal server outage.

## Decision

Use GRDB as a local cache and mutation queue. Persist user edits and queue entries in one local transaction. Push pending mutations in order, then pull a cursor-based change stream. Schedule a rolling window of notifications locally and confirm protected occurrence IDs to the server.

## Consequences

The app remains useful offline. Conflict UI is a core feature. Local storage may contain plaintext and relies on iOS device protection. Remote push becomes a fallback and synchronization mechanism instead of the only alarm path.
