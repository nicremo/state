# Agent integration

State is the durable reminder, task and context system for one owner. Every agent that
should capture or execute work talks to the same State server, but never under the same
identity as another agent.

## Overview

Each paired agent gets its own State identity:

- a **harness label** that names the agent product, for example `codex`,
- a **profile name** that names this particular installation of that product,
- an **actor** on the server, so every write is attributed to this agent alone,
- a **credential** stored in the operating system secret store, never in a plain file.

Two agents sharing one profile would share one actor, and the audit trail would no longer
answer which agent changed a reminder. Pair one profile per agent.

| Agent | Harness label | Integration |
| --- | --- | --- |
| Codex | `codex` | automatic |
| Claude Code | `claude-code` | automatic |
| OpenCode | `opencode` | automatic |
| Pi Agent | `pi` (also accepted: `pi-agent`) | manual |
| DeepSeek Harness | `deepseek-harness` | manual |

An **automatic** integration writes the MCP server entry and the rule block into the
agent's own configuration files, with a backup and a marked block that can be removed
again. A **manual** integration stores the credential and prints the MCP server entry and
the rule block, which the operator pastes into that agent's own configuration. `statectl`
never writes configuration for a product whose configuration layout it has not verified,
because a wrong guess corrupts another tool's settings.

Harness labels are validated on both the app and the CLI: two to thirty-two characters,
lower case letters, digits and inner hyphens, not starting or ending with a hyphen. A
label typo would otherwise create a second actor for the same agent.

## Pair an agent

1. Create a one-time pairing code as the owner:
   - State app, **Settings**, section **Connect an agent**: choose the harness from the
     picker or type a custom label, give the agent a name, then **Create one-time code**.
   - The Mac Server app creates codes for iPhone, Claude Code, Codex and OpenCode. For Pi
     Agent, DeepSeek Harness or any other label, create the code in the State app.
   The app shows the code, its expiry, and a copyable `statectl pair` command.
2. Run that command on the workstation:

```bash
statectl pair --server https://state.example.com --code ONE_TIME_CODE \
  --harness claude-code --profile claude-code
```

| Flag | Meaning |
| --- | --- |
| `--server` | Base URL of the State server the code was created on. |
| `--code` | The one-time pairing code, valid once and until it expires. |
| `--harness` | Agent label from the table above. Must match the code. |
| `--profile` | Local profile name. Defaults to the harness label. |
| `--install` | Defaults to true. Install the MCP configuration and the rules. |

Pairing stores the credential in the operating system secret store and writes the local
profile to `statectl.json` under the user configuration directory, which is
`~/Library/Application Support/State/statectl.json` on macOS. The command ends with
`paired <harness> as <profile>`.

## What statectl changes

For an automatic integration, `statectl pair` and `statectl install` write the MCP server
entry and the rule block into two files. Paths come from `DefaultInstallPaths()` in
`internal/statectl/installer.go`:

| Harness | MCP configuration | Agent rules |
| --- | --- | --- |
| `codex` | `~/.codex/config.toml` | `~/.codex/AGENTS.md` |
| `claude-code` | `~/.claude.json` | `~/.claude/CLAUDE.md` |
| `opencode` | `~/.config/opencode/opencode.json` | `~/.config/opencode/AGENTS.md` |

Properties of the change:

- **Codex** gets a marked block in its TOML configuration. The MCP entry lives between
  `# statectl:state:start` and `# statectl:state:end`, so unrelated TOML text is preserved.
- **Claude Code** and **OpenCode** keep their JSON configuration. `statectl` adds one entry
  under `mcpServers.state` or `mcp.state` and re-encodes the file, so unrelated values
  survive while key order, indentation and number formatting are normalized to sorted
  two-space JSON. Whitespace changes in your editor are therefore expected, lost comments
  are not, because JSON has none.
- The rule file of every automatic harness gets the rules between
  `<!-- statectl:state:start -->` and `<!-- statectl:state:end -->`. Text outside the block
  is preserved line by line.
- Installation is idempotent. Running `statectl pair` twice replaces the State entry and
  leaves every other setting and every other agent instruction in place.
- Before an existing file is rewritten, `statectl` copies it next to itself as
  `<file>.state-backup-<timestamp>`, for example
  `config.toml.state-backup-20260923T015600Z`. New files get mode `0600`.
- `statectl uninstall --harness <label>` removes the State entry from both files again and
  keeps the backup files. Unrelated content stays.

`statectl unpair --profile <name>` removes the local profile and the stored credential.
It also uninstalls the harness integration unless you pass `--uninstall=false`.

## Manual agents

For a harness without a shipped integration, `statectl pair` still stores the credential
and creates the profile. It then prints the MCP server entry and the rule block instead of
writing files. The command writes the statectl profile and the credential as always, but it
writes no configuration file of that agent.

The printed output has this shape:

```text
statectl has no shipped configuration for <harness>.
The credential is stored and the profile is ready. Add this MCP server
to that agent yourself:

{
  "mcpServers": {
    "state": {
      "args": [
        "mcp",
        "--profile",
        "<profile>"
      ],
      "command": "/usr/local/bin/statectl"
    }
  }
}

Agents that expect a command line instead of JSON use:

  /usr/local/bin/statectl mcp --profile <profile>

<product hint for a known manual agent>

Then add these rules to that agent's instruction file:

<the rules from DefaultAgentRules()>

Verify with: /usr/local/bin/statectl doctor --profile <profile>
```

Copy the two parts to the right places:

- the JSON object into the MCP server list of that agent, the same place where its other
  MCP servers are declared,
- the rules into the instruction file that agent loads at session start.

Two products have a prepared hint in the output:

- **Pi Agent** (`pi`, `pi-agent`): add the server to the `"mcpServers"` object of the
  profile's MCP configuration and the rules to the profile's `AGENTS.md` or system prompt
  file. Each Pi profile needs its own statectl profile.
- **DeepSeek Harness** (`deepseek-harness`): add the server to the MCP client plugin
  configuration of each preset that should see State, and the rules to that preset's
  persona or agent instructions.

Every other label gets no product hint, because State does not know that product's
configuration layout. The JSON definition, the command line variant, the rules and the
verification line are printed all the same.

Print the instructions again at any time with:

```bash
statectl install --harness <harness> --profile <profile>
```

## The capture protocol

The rules in the printed block are the operational form of the capture protocol in
[`universal-agent-todo-capture.md`](universal-agent-todo-capture.md) sections 6 and 7. In
short, a paired agent:

1. calls `get_briefing` at session start with the last known cursor and keeps the returned
   cursor for the next session,
2. captures only explicit intent, and asks once when it is unsure whether something
   belongs in State,
3. resolves date, local time, time zone, recurrence and execution intent before writing,
   and asks one short question instead of inventing a date or recurrence,
4. calls `create_reminder` exactly once per obligation with the original wording in
   `source_text` and a fresh UUIDv7 in `client_request_id`,
5. reports success only after the result says `stored=true`, and says plainly that nothing
   was saved on any error,
6. appends new context with `add_comment` and passes `expected_revision` on updates
   instead of rewriting history,
7. never deletes or archives reminders and never puts secrets, tokens or full logs into
   State,
8. falls back to `statectl reminder create` in the terminal when the MCP tools are
   unavailable,
9. never completes an occurrence manually when a runner policy already handles completion.

The full contract, including the required context sections and the ambiguity rules, is in
[`universal-agent-todo-capture.md`](universal-agent-todo-capture.md).

## Verify

```bash
statectl doctor --profile <profile>
```

The command reports the four facts that matter:

```text
server: <server version>
protocol: <MCP protocol version>
actor: <actor ID of this profile>
tools: <count> (<sorted tool names>)
```

If the actor is the expected one and the tool list contains `get_briefing`,
`create_reminder`, `update_reminder` and `add_comment`, the agent can capture into State.
