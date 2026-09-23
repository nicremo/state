# State Server for macOS

A native SwiftUI menu bar app that runs the existing Go server on the Mac.
Requires macOS 14 or later. The build script produces the current Mac architecture.

## Build and run

Requirements: Xcode, Swift 6, Go 1.25 or later.

```bash
bash macos/build.sh
open 'dist/State Server.app'
```

The Go server and `statectl` are bundled. Running the app needs neither Go nor
Docker. `STATE_CODESIGN_IDENTITY` optionally selects an installed signing identity;
the default is a local ad hoc signature, suitable for this Mac. Distribution to
other Macs requires appropriate signing and notarization.

Install the app in a stable location before enabling **Bei der Anmeldung starten**.
macOS manages that login item using `SMAppService.mainApp`. If approval is required,
the app links to Login Items in System Settings.

## Behavior

- Starts a separate server process and shows its status every five seconds.
- Closing the window leaves the menu bar app and server running.
- Stopping the server or quitting the app shuts down the database cleanly.
- Restarts an unexpected process exit with exponential backoff from two seconds up to
  one minute and never gives up. A run of at least one minute counts as healthy and
  resets the backoff.
- Treats sleep and wake as normal. The server pauses with the Mac, so the silence during
  sleep is not a hang, and the app asks for a fresh status after waking.
- Writes the server's structured events to a rotating log file under
  `~/Library/Logs/State Server`. Request bodies, credentials and pairing payloads are
  never persisted.
- Exiting or crashing the parent closes its private pipe and stops the server.
- Autostart is optional. No source code or program updates run automatically.
- Existing iOS client installations and remote servers are not migrated or altered.

Data is stored independently in `~/Library/Application Support/State Server`.
The directory is private to the current user. Preserve this entire folder when
backing up, including the desktop TLS identity. Identity loss requires pairing again.
No data or pairing codes are written into the source repository or app bundle.

## Local connections

| Client | Address | Authentication |
| --- | --- | --- |
| iPhone | `https://<Mac Bonjour name>.local:9847` | Certificate pin from QR plus per-device credential |
| Local programs | `http://127.0.0.1:9848/mcp` | Per-program credential |
| Mac management UI | Inherited process pipes | Parent process only |

Port 9847 accepts requests from private, loopback and link-local addresses only.
Port 9848 binds exclusively to loopback. There is no HTTP management endpoint,
router configuration, public tunnel, or external relay required for local sync.
The Mac may ask for local-network or incoming-connection permission.

Device codes are valid for ten minutes and can be exchanged once. A consumed
iPhone code is removed from the UI after the next status update. Expired codes
are renewed while the pairing panel is visible. Hiding a QR does not revoke its
code; it remains valid until consumed or expired. Program codes can be copied as
a `statectl pair` command; running that command installs the selected integration.
The runner option mints a runner code instead and copies a `state-runner pair`
command that also installs the runner as a launch agent (see
[runner-service](../docs/runner-service.md)); `state-runner` must be installed with
`scripts/install-agent-tools.sh` first.

The iPhone must run the updated client in this repository. The existing released
client does not understand the QR certificate fingerprint and will reject the
self-signed certificate. The updated client stores the pin with its server session,
accepts only that exact certificate at that origin and rejects redirects. Standard
HTTPS connections and older pairing QR codes remain supported.

Renaming the Mac changes its Bonjour name, so the server replaces the stored
certificate for the new name. The fingerprint changes with it, and paired iPhones
pair again once with the new QR code.

## Optional push relay

The Mac server can advertise a public push relay, for example a State relay on a VPS, so a paired
iPhone receives notifications outside the home network while the Mac stays reachable. Enter the
address under **Push unterwegs (optional)** in the app; **Übernehmen** stores it and restarts the
server, and the pairing QR code then carries it as the `relay` parameter. The iPhone keeps that
address with its server session, so switching servers cannot inherit the address of an earlier
connection, and the relay only forwards sealed envelopes, which keeps the content end to end
encrypted. Leave the field empty for local-only delivery, because the server never requires a
relay and never contacts one itself.

## Background and offline limits

The server runs while the user is logged in and the Mac is awake. It does not
prevent sleep. The iPhone connects on opening or returning to the app and polls
every 15 seconds while the app is active and paired with a local certificate.
iOS does not permit uninterrupted arbitrary background polling. Previously synced
reminders and local notifications remain available offline. New remote reminders
arrive when the app can synchronize again. This local setup does not configure
APNs or claim delivery to a suspended iPhone or outside the local network.

## Troubleshooting

Server events are written to `~/Library/Logs/State Server/server.log`. The file and its
directory are readable by the current user only. It rotates at 5 MB and keeps
`server.log.1` to `server.log.3` next to the current file. The Diagnose area of the
window shows the last exit code and the number of consecutive failed starts, and the
window and menu both offer **Log im Finder zeigen** and **Log anzeigen**.

An unexpected exit is retried with exponential backoff: 2, 4, 8, 16, 32 and then 60
seconds. The app never gives up. A port conflict or a crash loop therefore shows up as
repeated restarts instead of a server that stays silently down. Stopping the server
resets the backoff.

Sleep is normal and not a fault. The server pauses with the Mac, so missing status
lines during sleep do not mark it unresponsive. After waking, the app asks for a fresh
status and starts the server again only if the process is gone.

**Bei der Anmeldung starten** registers the app through `SMAppService.mainApp`. Check
the registration with:

```bash
sfltool dumpbtm | grep -i state
```

The command may ask for administrator rights. Signed builds can also be checked in
System Settings under General > Login Items.

Check both listeners when the iPhone cannot reach the Mac:

```bash
lsof -nP -iTCP:9847 -sTCP:LISTEN
lsof -nP -iTCP:9848 -sTCP:LISTEN
```

## Verification

```bash
go test -race ./...
swift test
bash macos/build.sh
python3 macos/verify-local.py
cd ios
xcodegen generate
xcodebuild -project State.xcodeproj -scheme State \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' \
  CODE_SIGNING_ALLOWED=NO test
```

The local integration script uses a fresh temporary database and a real TLS
listener. It verifies the shared Swift trust implementation with the correct pin,
wrong pin and wrong origin, then checks clean shutdown on parent-pipe closure.
The Go integration tests cover one-time pairing, API authentication, LAN filtering,
certificate persistence, paired-device persistence and sync access after restart.
