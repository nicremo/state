import SwiftUI
import UIKit

/// The setup guide the owner needs before the connection form makes any sense.
/// It lives in the app rather than only on the web, because the moment it is
/// needed most is the moment before a server exists.
struct DocumentationView: View {
    var body: some View {
        List {
            Section {
                VStack(alignment: .leading, spacing: StateTheme.Space.group) {
                    Text("State is a memory your coding agents write into and you own.")
                        .font(.headline)
                        .foregroundStyle(StateTheme.graphite)
                    Text("You run the server. Your agents write reminders through it. This iPhone reads them, works offline and alerts you when something is due.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                .padding(.vertical, StateTheme.Space.snug)
            }

            Section("Step 1 · Run the server") {
                DocumentationStep(
                    number: 1,
                    title: String(localized: "Start State on a machine you control"),
                    detail: String(localized: "A Mac, a Linux box or a small VPS. One binary, no database to install.")
                )
                DocumentationCode(
                    "STATE_DATA_DIR=./state_data \\\n  STATE_HTTP_ADDR=127.0.0.1:8090 \\\n  ./state-server serve"
                )
                DocumentationStep(
                    number: 2,
                    title: String(localized: "Print the bootstrap token"),
                    detail: String(localized: "It is the one time secret that makes the first person the owner.")
                )
                DocumentationCode("./state-server bootstrap-token --data ./state_data")
            }

            Section("Step 2 · Connect this iPhone") {
                DocumentationStep(
                    number: 3,
                    title: String(localized: "Enter the server address"),
                    detail: String(localized: "The full HTTPS address of your server, for example https://state.example.com.")
                )
                DocumentationStep(
                    number: 4,
                    title: String(localized: "Paste the bootstrap token once"),
                    detail: String(localized: "Choose First setup on the connection screen and paste it there. This iPhone becomes the owner and stores its credential in the iOS Keychain.")
                )
                DocumentationNote(
                    systemImage: "qrcode.viewfinder",
                    text: String(localized: "The QR code is a shortcut for the same two values. Anything that prints a State pairing QR code, your server's setup output for instance, can be scanned instead of typing.")
                )
            }

            Section("Step 3 · Connect your agents") {
                DocumentationStep(
                    number: 5,
                    title: String(localized: "Create a one time code"),
                    detail: String(localized: "In Settings, under Connect an agent, pick the agent and tap Create one time code. The code is valid for a short while and for exactly one pairing.")
                )
                DocumentationStep(
                    number: 6,
                    title: String(localized: "Run one command per agent"),
                    detail: String(localized: "On your Mac or Windows machine. Every agent gets its own credential and shows up separately in the history.")
                )
                DocumentationCode(
                    "statectl pair \\\n  --server https://state.example.com \\\n  --code ONE-TIME-CODE \\\n  --harness codex"
                )
                DocumentationNote(
                    systemImage: "terminal",
                    text: String(localized: "Codex, Claude Code and OpenCode are configured automatically. Any other agent pairs the same way and statectl prints the MCP entry for you to paste.")
                )
            }

            Section {
                Link(destination: StateLinks.documentation) {
                    Label("Read the full documentation", systemImage: "book")
                }
                Link(destination: StateLinks.repository) {
                    Label("Source code on GitHub", systemImage: "chevron.left.forwardslash.chevron.right")
                }
            } footer: {
                Text("The full guide covers deployment, backups, notification delivery and how to revoke an agent.")
            }
        }
        .listStyle(.insetGrouped)
        .stateBackground()
        .navigationTitle("Documentation")
        .navigationBarTitleDisplayMode(.inline)
    }
}

private struct DocumentationStep: View {
    let number: Int
    let title: String
    let detail: String

    var body: some View {
        HStack(alignment: .top, spacing: StateTheme.Space.group) {
            Text(number, format: .number)
                .font(.caption.monospacedDigit().weight(.semibold))
                .foregroundStyle(StateTheme.accent)
                .frame(width: 24, height: 24)
                .background(StateTheme.accentSoft, in: Circle())

            VStack(alignment: .leading, spacing: StateTheme.Space.tight) {
                Text(title)
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(StateTheme.graphite)
                Text(detail)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.vertical, StateTheme.Space.tight)
        .accessibilityElement(children: .combine)
    }
}

private struct DocumentationCode: View {
    let command: String
    @State private var copied = false

    init(_ command: String) {
        self.command = command
    }

    var body: some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.inner) {
            // Menlo rather than the system monospace, because SF Mono renders
            // a double hyphen as one long dash and a command line flag has to
            // read exactly as it is typed.
            Text(verbatim: command)
                .font(.custom("Menlo", size: 12, relativeTo: .caption))
                .foregroundStyle(StateTheme.graphite)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)

            Button {
                UIPasteboard.general.string = command.replacingOccurrences(of: "\\\n  ", with: "")
                withAnimation(StateTheme.stateChange) { copied = true }
            } label: {
                Label(
                    copied ? String(localized: "Copied") : String(localized: "Copy"),
                    systemImage: copied ? "checkmark" : "doc.on.doc"
                )
                .labelStyle(.tight)
                .font(.caption.weight(.medium))
            }
            .buttonStyle(.plain)
            .foregroundStyle(copied ? Color.green : StateTheme.accent)
        }
        .padding(StateTheme.Space.group)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(StateTheme.accentSoft.opacity(0.6), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        .padding(.vertical, StateTheme.Space.tight)
    }
}

private struct DocumentationNote: View {
    let systemImage: String
    let text: String

    var body: some View {
        HStack(alignment: .top, spacing: StateTheme.Space.inner) {
            Image(systemName: systemImage)
                .font(.caption)
                .foregroundStyle(.secondary)
                .frame(width: 18)
            Text(text)
                .font(.footnote)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.vertical, StateTheme.Space.tight)
        .accessibilityElement(children: .combine)
    }
}
