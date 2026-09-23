# Local Mac connection

The iPhone app can connect to State Server on the Mac using the same network.
Install the local-server-compatible TestFlight build before scanning the QR.
The previous build, 2608162145, reads the server address and code but does not
understand the certificate fingerprint. It rejects the Mac's self-signed certificate.

## Pair

1. Keep the Mac awake with State Server running.
2. Connect the iPhone to the same local network and allow State local-network access.
3. Open State Server on the Mac and show the iPhone pairing QR code.
4. In the updated iPhone app, scan the QR and tap Connect.

The QR carries the HTTPS server address, a one-time code and the SHA-256
fingerprint of the local server certificate. The app stores that fingerprint
with the server session. Authentication credentials remain in Keychain.
Only the matching certificate at the selected origin is accepted. Redirects
and certificate changes are rejected. No system-wide certificate trust is added.

State synchronizes on foreground and every 15 seconds while the app is active.
The Mac must be awake and reachable. Previously synchronized reminders and local
notifications remain available offline; no continuous iOS background polling is promised.

## Push outside the home network

A Mac server stays in the local network, so the iPhone cannot reach it while it is away.
The optional State relay closes that gap: the iPhone registers its APNs route at a public
relay, and the Mac server sends end to end encrypted envelopes to that address. The relay
only forwards sealed envelopes and never sees plaintext (see
`docs/adr/0003-plaintext-blind-push-relay.md`), and the Mac needs outbound access to the
relay, no inbound connection from the internet.

Run the relay from the VPS deploy kit in `deploy/` and give it its own public name, for
example `https://relay.example.com`. On the Mac open State Server, enter that address under
**Push unterwegs (optional)** and press **Übernehmen**. The app stores the address as
`state.desktop.relay-url`, restarts the server with `--relay-url`, and the status line and
every new pairing QR code then carry it as the `relay` parameter. An empty field keeps the
server in local-only mode, and the server rejects anything that is not an absolute HTTPS
address without credentials, query or fragment.

The iPhone stores the address with the server session, not globally. Scanning the QR code
copies it into the session, and the **Push relay** section in the settings shows it, changes
it or removes it later. A session without a relay does not register for push at all: the
settings then say `Kein Relay: Mitteilungen nur lokal und im WLAN`, which is the normal
state for a Mac in the home network. Addresses are never derived from a `.local` name or a
private IP address any more, because `relay.<name>.local` does not exist and a failed
registration would look like a broken relay.

## Release evidence

- Version 1.0.0, build 2609140030.
- Based on the existing iPhone interface at b5dbe60, preserving its onboarding and layout.
- 17 iOS unit tests passed, including local QR parsing and legacy QR compatibility.
- The TLS verifier is byte-identical to the implementation verified against a real
  local server with accepted, incorrect and wrong-origin certificate pins.
- The signed IPA includes local-network permission text and the local ATS exception.
- IPA SHA-256: `5697b106ae3bdac900c4a795b63ff29f3c6e4b5bc8071438443c605c74eb94b5`.

TestFlight availability must be read back separately using
`BUILD_NUMBER=2609140030 fastlane ios local_update_status` after upload and processing.

Readback on 2026-09-14 at 02:36 CEST confirmed `VALID`, `IN_BETA_TESTING`,
and assignment to the existing Internal group. The uploader attempted an
unnecessary external beta-review submission after processing; Apple rejected
that request because external beta metadata was absent. Internal availability
was unaffected. The upload lane now explicitly disables beta-review submission.
