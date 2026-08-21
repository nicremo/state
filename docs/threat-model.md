# Threat model

## Protected assets

- Reminder and comment content.
- Full audit history and source excerpts.
- Owner, device, harness and runner credentials.
- Device notification private keys.
- APNs signing key and device tokens.
- Audit signing key and encrypted backups.
- Task contracts and agent run results.
- Local run artifacts under `.state/runs/` on owner workstations.

## Trust assumptions

- The owner-controlled State server is trusted with plaintext reminder content.
- The iPhone and its Keychain are trusted while uncompromised.
- Agent harnesses can act only within their granted mutation scope.
- A paired workstation is trusted to execute only what its registered projects, adapters and policies allow.
- The shared relay, network intermediaries and notification logs are not trusted with plaintext.
- Apple is trusted to operate APNs and App Attest according to its platform contract.

## Main threats and controls

| Threat | Control |
| --- | --- |
| Stolen harness credential | Per-harness identity, Keychain storage, rotation and revocation |
| Replay of an offline write | Stable request ID and server-side idempotency |
| Lost update | Required expected revision and HTTP 409 conflict |
| Agent deletes owner data | Agent permission model excludes archive and delete |
| Audit record alteration | Immutable trigger, signed hash chain and verification command |
| Relay reads reminder content | X25519, HKDF and AES-GCM before relay submission |
| Relay database disclosure | Opaque routes and encrypted APNs tokens at rest |
| Fake relay registrations | App Attest assertion verification and rate limits |
| Push decryption failure | Generic fallback text without reminder content |
| Server outage at due time | Rolling local notification schedule on each iPhone |
| Backup disclosure | age encryption and external identity storage |
| Malicious stale client | Revision checks, actor authorization and auditable rejection |
| Stolen runner credential | Separate runner actor kind, narrow claim-only scope, rotation and revocation |
| Server sends arbitrary commands to a workstation | Task contracts carry no commands; the runner maps adapter plus contract to its own local invocation |
| Malicious or tampered task contract | Hash-pinned contract, policy revision pin and local policy validation before launch |
| Lease takeover by another runner | Claims bind to the claiming runner, compare-and-set transitions and lease expiry |
| Policy bypass into risky capabilities | Server-side capability vocabulary and the unattended low-risk allow-list; local approval gate for anything else |
| Run log disclosure | Artifacts stay on the workstation, summaries are redacted and bounded, push text is generic |
| Adapter supply-chain compromise | Adapters are thin wrappers over locally installed tools the owner chose and maintains |

## Residual risks

- A compromised owner server can read and alter plaintext before audit generation.
- A compromised unlocked iPhone can expose locally cached reminders.
- A compromised harness process can use its credential until revocation.
- A compromised workstation can execute anything its registered policies allow until the owner revokes the runner or narrows its scopes.
- Run success evidence relies on adapter exit codes and self-reported results.
- Notification timing and delivery metadata can reveal activity patterns even when content is encrypted.
- App Attest reduces automated abuse but does not guarantee that a genuine device is controlled by the expected person.

## Out of scope for version 1

End-to-end encrypted search, multi-owner teams, attachment scanning, voice input, embeddings and encrypted upload of run artifacts are intentionally excluded. Scheduled agent execution is in scope since the agent execution extension and is documented in `docs/agent-execution-extension.md`.
