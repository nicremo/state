# Threat model

## Protected assets

- Reminder and comment content.
- Full audit history and source excerpts.
- Owner, device and harness credentials.
- Device notification private keys.
- APNs signing key and device tokens.
- Audit signing key and encrypted backups.

## Trust assumptions

- The owner-controlled State server is trusted with plaintext reminder content.
- The iPhone and its Keychain are trusted while uncompromised.
- Agent harnesses can act only within their granted mutation scope.
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

## Residual risks

- A compromised owner server can read and alter plaintext before audit generation.
- A compromised unlocked iPhone can expose locally cached reminders.
- A compromised harness process can use its credential until revocation.
- Notification timing and delivery metadata can reveal activity patterns even when content is encrypted.
- App Attest reduces automated abuse but does not guarantee that a genuine device is controlled by the expected person.

## Out of scope for version 1

End-to-end encrypted search, multi-owner teams, attachment scanning, voice input, embeddings and remote agent execution are intentionally excluded.
