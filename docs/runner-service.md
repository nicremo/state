# The state runner as a background service

`state-runner` is the process that picks up agent runs for this Mac. The Mac app
(`State` from the App Store and TestFlight) runs in the App Sandbox, so it cannot
launch `claude`, `codex`, `opencode` or `pi` inside project folders and it cannot
write into `~/Library/LaunchAgents`. The runner therefore runs outside the app as
a per-user LaunchAgent that `state-runner service install` creates.

The app never installs anything itself. It shows the paired runners with their
online status and hands you the full command to copy.

## What the agent does

`state-runner service install` writes exactly one file,
`~/Library/LaunchAgents/com.fabincrm.state.runner.plist`, loads it with
`launchctl bootstrap gui/<uid>` and keeps it loaded:

| Key | Value | Effect |
| --- | --- | --- |
| `Label` | `com.fabincrm.state.runner` | launchd identity of the agent |
| `ProgramArguments` | `state-runner run --config <config>` | the runner polls the paired server |
| `RunAtLoad` | `true` | starts at login |
| `KeepAlive` | `true` | launchd restarts the process after a crash |
| `ThrottleInterval` | `30` | a crash loop restarts at most every 30 seconds |
| `ProcessType` | `Background` | the agent yields to interactive work |
| `StandardOutPath` / `StandardErrorPath` | `~/Library/Logs/State Runner/runner.{out,err}.log` | runner logs |
| `EnvironmentVariables` | `PATH`, `HOME` | adapters find the agent CLIs and their own config |

The `PATH` in the agent is your shell `PATH` plus `~/.local/bin`,
`/opt/homebrew/bin` and `/usr/local/bin`, without duplicates. launchd starts
agents without a login shell, so this is what makes `claude`, `codex` or
`opencode` resolvable for the runner.

## Installing

1. Install the command line tools. From the repository root:

   ```bash
   scripts/install-agent-tools.sh
   ```

   The script builds `statectl` and `state-runner` with the short commit hash as
   version and copies both into `~/.local/bin`. It prints the `~/.zshrc` line if
   that directory is not on your `PATH` yet, and it does not edit your shell
   profile itself.

2. Open the app, go to Settings, Runners, type a runner name and create a
   one-time code. The server rejects a code that was created for a harness
   profile, so always use the code from the Runners section.

3. Copy the command with the copy button. It pairs this machine and installs the
   agent in one step:

   ```bash
   state-runner pair --server https://state.example.com --code ABC123 \
     --name mac-mini --adapters claude-code,codex --work-root "$HOME/Projects" \
     && state-runner service install
   ```

   Adjust three things before you run it:

   - `--work-root`: the directory that holds your project checkouts. Every run
     the runner executes stays inside this root.
   - `--adapters`: the harnesses this machine may launch. Use the slugs the app
     lists, for example `claude-code,codex,opencode`.
   - `--name`: the display name in the app. Any unique name works.

   `state-runner pair` stores the runner credential in the login keychain and
   writes `~/Library/Application Support/State/runner.json`. The runner config is
   written with mode `0600`, the credential never lands in the config file.

4. `state-runner service install` refuses to run when the config is missing, so
   the order of the two commands matters. On any non-macOS system it returns:

   ```text
   service management is only implemented for macOS; use systemd on Linux (see docs/runner-service.md)
   ```

## Status and logs

```bash
state-runner service status
```

The command asks `launchctl print gui/<uid>/com.fabincrm.state.runner` and prints
`state-runner is running (com.fabincrm.state.runner)` when launchd reports
`state = running`, otherwise `state-runner is not running` with the launchctl
state line.

The app shows the same information per runner: a green dot and `Online` while the
last heartbeat is younger than two minutes, otherwise a grey dot with the
relative last seen time. The runner sends a heartbeat while it polls, so an
online runner needs no extra request.

Logs:

```bash
tail -f ~/Library/Logs/State\ Runner/runner.out.log
tail -f ~/Library/Logs/State\ Runner/runner.err.log
```

`runner.err.log` carries the runner log output, `runner.out.log` only carries
what the process prints to stdout. Launchd does not rotate these files, so
truncate them or delete them when they get large; launchd recreates them.

## Restarting after a change

The agent keeps the config it was installed with. After editing
`~/Library/Application Support/State/runner.json` for a new server URL or a new
work root, reload the agent:

```bash
state-runner service install     # bootout, write, bootstrap: idempotent
```

Installing twice is safe: the command boots the old agent out first and ignores a
"service not loaded" error.

## Uninstalling

```bash
state-runner service uninstall
```

This boots the agent out and removes the plist. It is silent when nothing was
installed. To remove this machine from the server as well, revoke the runner in
the app (Settings, Runners, the x button), and remove the credential from the
keychain:

```bash
security delete-generic-password -s com.fabincrm.state.statectl -a "runner:<name>:<hash>"
```

`com.fabincrm.state.statectl` is the keychain service the tooling shares. The
account is `runner:<runner name>:<hash>`: the hash is the first 8 bytes of the
SHA-256 of the server URL in hex, which is exactly what `RunnerConfig.CredentialAccount()`
builds. `security dump-keychain | grep runner:` lists the existing accounts, or
re-run `state-runner pair` with the same server and name to refresh the entry.
Deleting `~/Library/Application Support/State/runner.json` finishes the cleanup.

## Linux and other systems

`state-runner service` is macOS only. On Linux, run the same binary as a systemd
user unit:

```ini
# ~/.config/systemd/user/state-runner.service
[Unit]
Description=State runner
After=network-online.target

[Service]
ExecStart=%h/.local/bin/state-runner run
Restart=always
RestartSec=5
Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now state-runner.service
systemctl --user status state-runner.service
```

`state-runner pair` works on Linux unchanged, including the keychain-backed
credential store. The documented macOS commands in this file do not apply.

## Security boundaries

- **Outgoing only.** The runner polls the server it was paired with
  (`POST /api/v1/runner/claims` with a bounded long poll) and never listens on a
  port. Nothing on the network can reach it, so there is no inbound attack
  surface and no need for a firewall rule.
- **Own credential.** The runner uses a runner actor with its own keychain entry,
  separate from harness profiles and from the owner token. Revoking the runner in
  the app immediately stops claims.
- **Work root.** Every agent process runs inside the configured `--work-root`.
  The server sends a project ID and a task contract, never a shell command, and
  the runner resolves the working directory itself.
- **Least privilege at login.** The agent runs as your user, because the agent
  CLIs need your repositories and your API credentials. It does not run as root,
  and the plist contains no `sudo`, no shell string and no secret: only the
  executable, the config path, the log paths, `PATH` and `HOME`.
- **No secrets in the plist.** The runner credential lives in the login keychain
  and is read at startup. The launch agent file is world readable (`0644`) on
  purpose, and it must stay free of tokens.

See `docs/universal-agent-todo-capture.md` and `docs/threat-model.md` for the
full trust boundary of agent execution.
