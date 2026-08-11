# ADR 0001: Monorepo and embedded PocketBase

Status: accepted

## Context

State needs one deployable personal server, a separate relay, a CLI and a native iOS app. The personal server requires transactions, migrations, full-text search and a compact self-hosting footprint.

## Decision

Use one Apache-2.0 monorepo. Build `state-server` as a Go binary with embedded PocketBase and SQLite. Keep the relay as a separate Go process and keep the iOS client native.

## Consequences

Operators deploy one personal-server binary and one optional relay binary. SQLite keeps backup and restore simple. PocketBase internals remain behind repository interfaces so domain invariants do not depend on its HTTP API.
