# State

State is a self-hosted reminder and context service for coding agents. It keeps reminders, comments, revisions, conflicts, and a complete audit history in sync between Codex, Claude Code, OpenCode, and a native iOS app.

## Status

The initial implementation is under active development. The repository will contain:

- `state-server`, a Go service with embedded PocketBase, REST, MCP, scheduling, search, and audit history
- `state-relay`, a plaintext-blind APNs relay
- `statectl`, a local pairing and STDIO adapter for agent harnesses
- `State`, a native SwiftUI app for iOS 18 and later

## Security model

The personal State server can read reminder content so MCP tools and full-text search work. Push payloads are encrypted for the target device before they reach the shared relay. Harnesses receive separate revocable credentials and cannot delete or archive reminders.

## License

Apache-2.0

