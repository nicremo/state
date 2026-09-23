import SwiftUI
#if os(iOS)
import UIKit
import VisionKit
#elseif os(macOS)
import AppKit
#endif

/// Everything needed to pair this device with a server. Shared by the three
/// connection screens so a scanned code, a typed address and the confirmation
/// all edit one draft.
@MainActor
@Observable
final class ConnectDraft {
    enum Method: Hashable {
        case pairingCode
        case bootstrap
    }

    var method: Method = .pairingCode
    var server = ""
    var bootstrapToken = ""
    var pairingCode = ""
    var displayName = Platform.ownerName
    var deviceName = Platform.deviceName
    var certificateFingerprint: String?
    var scannedServer = ""
    var scannedRelayURL: URL?

    var serverURL: URL? {
        let value = server.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: value), url.scheme != nil, url.host != nil else { return nil }
        return url
    }

    var secret: String {
        (method == .bootstrap ? bootstrapToken : pairingCode).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var canConnect: Bool {
        serverURL != nil
            && !secret.isEmpty
            && !displayName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !deviceName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    /// Whether the address still belongs to the scanned code, so its pinned
    /// certificate and relay may be used.
    var usesScannedServer: Bool {
        !scannedServer.isEmpty && server.trimmingCharacters(in: .whitespacesAndNewlines) == scannedServer
    }

    /// Applies a scanned or pasted pairing link. Returns false for anything
    /// that is not a State pairing payload.
    func apply(_ value: String) -> Bool {
        guard let payload = PairingPayload(value: value.trimmingCharacters(in: .whitespacesAndNewlines)) else {
            return false
        }
        server = payload.serverURL.absoluteString
        scannedServer = server
        certificateFingerprint = payload.certificateFingerprint
        scannedRelayURL = payload.relayURL
        if let token = payload.bootstrapToken, !token.isEmpty {
            bootstrapToken = token
            method = .bootstrap
        }
        if let code = payload.pairingCode, !code.isEmpty {
            pairingCode = code
            method = .pairingCode
        }
        return true
    }
}

/// The first screen after the welcome. One statement, one way forward, and
/// the manual route one tap away for when there is no code to scan.
struct ConnectView: View {
    @Bindable var model: AppModel

    private enum Route: Hashable {
        case manual
        case confirm
    }

    @State private var draft = ConnectDraft()
    @State private var path: [Route] = []
    @State private var scansCode = false
    @State private var clipboardMessage: String?

    var body: some View {
        NavigationStack(path: $path) {
            landing
                .navigationDestination(for: Route.self) { route in
                    switch route {
                    case .manual:
                        ManualConnectView(model: model, draft: draft)
                    case .confirm:
                        ConfirmConnectView(model: model, draft: draft)
                    }
                }
        }
        .sheet(isPresented: $scansCode) { scanner }
    }

    // MARK: Landing

    private var landing: some View {
        #if os(macOS)
        // One centered block: on a Mac the actions belong right under the
        // statement, not at the bottom edge of a large window.
        VStack(spacing: 0) {
            Spacer(minLength: StateTheme.Space.stage)
            hero
            actions
                .frame(width: 320)
                .padding(.top, 36)
            footer
                .padding(.top, StateTheme.Space.section)
            Spacer(minLength: StateTheme.Space.stage)
        }
        .padding(StateTheme.Space.stage)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(StateTheme.ground.ignoresSafeArea())
        #else
        VStack(spacing: 0) {
            Spacer(minLength: StateTheme.Space.stage)
            hero
            Spacer(minLength: StateTheme.Space.stage)
            actions
                .frame(maxWidth: 440)
            footer
                .padding(.top, StateTheme.Space.section)
        }
        .padding(.horizontal, StateTheme.Space.stage)
        .padding(.bottom, StateTheme.Space.block)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(StateTheme.ground.ignoresSafeArea())
        .toolbar(.hidden, for: .navigationBar)
        #endif
    }

    private var hero: some View {
        VStack(spacing: StateTheme.Space.section) {
            StateMark(size: 80)

            VStack(spacing: StateTheme.Space.inner) {
                Text("Connect your server")
                    .font(.title.weight(.bold))
                    .foregroundStyle(StateTheme.graphite)
                    .fixedSize(horizontal: false, vertical: true)
                Text(landingSubtitle)
                    .font(.body)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .multilineTextAlignment(.center)
        }
        .frame(maxWidth: 340)
    }

    private var actions: some View {
        VStack(spacing: StateTheme.Space.group) {
            if hasCodeAction {
                primaryAction
                Button {
                    path.append(.manual)
                } label: {
                    Text("Enter address")
                }
                .buttonStyle(.stateSecondary)
                .accessibilityIdentifier("enter-address")
            } else {
                Button {
                    path.append(.manual)
                } label: {
                    Text("Enter address")
                }
                .buttonStyle(.statePrimary)
                .accessibilityIdentifier("enter-address")
            }

            if let clipboardMessage {
                Text(clipboardMessage)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
                    .transition(.opacity)
            }
        }
        .animation(StateTheme.stateChange, value: clipboardMessage)
    }

    private var landingSubtitle: String {
        #if os(macOS)
        String(localized: "Paste the pairing link your State server shows, or enter its address.")
        #else
        String(localized: "Scan the pairing code your State server shows, or enter its address.")
        #endif
    }

    /// Whether this device can take a pairing code directly: a camera
    /// scanner on iPhone and iPad, the clipboard on the Mac.
    private var hasCodeAction: Bool {
        #if os(iOS)
        DataScannerViewController.isSupported
        #else
        true
        #endif
    }

    @ViewBuilder
    private var primaryAction: some View {
        #if os(iOS)
        if DataScannerViewController.isSupported {
            Button {
                scansCode = true
            } label: {
                Label(String(localized: "Scan pairing code"), systemImage: "qrcode.viewfinder")
            }
            .buttonStyle(.statePrimary)
            .accessibilityIdentifier("scan-pairing-code")
        }
        #else
        Button {
            pastePairingLink()
        } label: {
            Label(String(localized: "Paste pairing link"), systemImage: "doc.on.clipboard")
        }
        .buttonStyle(.statePrimary)
        .keyboardShortcut("v", modifiers: .command)
        .accessibilityIdentifier("paste-pairing-link")
        #endif
    }

    private var footer: some View {
        HStack(spacing: StateTheme.Space.section) {
            NavigationLink {
                DocumentationView()
            } label: {
                Text("No server yet?")
            }
            .accessibilityIdentifier("open-documentation")

            Button {
                Task { await model.enterDemo() }
            } label: {
                Text("Try without a server")
            }
            .accessibilityIdentifier("explore-demo")
        }
        .buttonStyle(.plain)
        .font(.footnote.weight(.medium))
        .foregroundStyle(.secondary)
    }

    // MARK: Scanning

    @ViewBuilder
    private var scanner: some View {
        #if os(iOS)
        NavigationStack {
            PairingScannerView { value in
                scansCode = false
                accept(value)
            }
            .ignoresSafeArea(edges: .bottom)
            .navigationTitle("Scan pairing code")
            .stateInlineNavigationTitle()
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { scansCode = false }
                }
            }
        }
        #endif
    }

    private func pastePairingLink() {
        #if os(macOS)
        let value = NSPasteboard.general.string(forType: .string) ?? ""
        if draft.apply(value) {
            clipboardMessage = nil
            path.append(.confirm)
        } else {
            clipboardMessage = String(localized: "No pairing link on the clipboard. Copy it in the State Server app first.")
        }
        #endif
    }

    private func accept(_ value: String) {
        guard draft.apply(value) else {
            model.presentedError = String(localized: "This is not a valid State pairing code.")
            return
        }
        path.append(.confirm)
    }
}

// MARK: - Confirmation after a scan

/// A scanned code already carries the server and the secret, so all that is
/// left is who is connecting.
private struct ConfirmConnectView: View {
    @Bindable var model: AppModel
    @Bindable var draft: ConnectDraft

    var body: some View {
        Form {
            Section {
                HStack(spacing: StateTheme.Space.group) {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.title2)
                        .symbolRenderingMode(.palette)
                        .foregroundStyle(StateTheme.onAccent, StateTheme.accent)
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: StateTheme.Space.hairline) {
                        Text(draft.serverURL?.host ?? draft.server)
                            .font(.headline)
                            .foregroundStyle(StateTheme.graphite)
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Text(draft.certificateFingerprint == nil
                             ? String(localized: "Pairing code received.")
                             : String(localized: "Local Mac. Certificate verified from the pairing code."))
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }
                .padding(.vertical, StateTheme.Space.tight)
            }

            DeviceIdentitySection(draft: draft)
        }
        .formStyle(.grouped)
        .stateReadableWidth()
        .stateBackground()
        .navigationTitle(String(localized: "Server found"))
        .stateInlineNavigationTitle()
        .safeAreaInset(edge: .bottom) {
            ConnectButton(model: model, draft: draft)
        }
    }
}

// MARK: - Manual entry

/// The long way, for a server without a code on screen: address, access and
/// this device, grouped the way Settings groups a new account.
private struct ManualConnectView: View {
    @Bindable var model: AppModel
    @Bindable var draft: ConnectDraft
    @FocusState private var focusedField: Field?

    private enum Field: Hashable {
        case server
        case secret
    }

    var body: some View {
        Form {
            Section {
                TextField(String(localized: "Server address"), text: $draft.server, prompt: Text(verbatim: "https://state.example.com"))
                    .textContentType(.URL)
                    .stateURLKeyboard()
                    .stateNoAutocapitalization()
                    .autocorrectionDisabled()
                    .submitLabel(.next)
                    .focused($focusedField, equals: .server)
                    .onSubmit { focusedField = .secret }
            } header: {
                Text("Server")
            }

            Section {
                Picker(String(localized: "Access"), selection: $draft.method.animation(StateTheme.stateChange)) {
                    Text("Pairing code").tag(ConnectDraft.Method.pairingCode)
                    Text("First setup").tag(ConnectDraft.Method.bootstrap)
                }
                .pickerStyle(.segmented)
                .labelsHidden()

                switch draft.method {
                case .pairingCode:
                    SecureField(String(localized: "Pairing code"), text: $draft.pairingCode)
                        .textContentType(.oneTimeCode)
                        .focused($focusedField, equals: .secret)
                case .bootstrap:
                    SecureField(String(localized: "Bootstrap token"), text: $draft.bootstrapToken)
                        .focused($focusedField, equals: .secret)
                }
            } header: {
                Text("Access")
            } footer: {
                Text(draft.method == .pairingCode
                     ? String(localized: "Create a code in State on a device that is already connected, under Settings.")
                     : String(localized: "Your server prints the token once with state-server bootstrap-token. This device becomes the owner."))
            }

            DeviceIdentitySection(draft: draft)
        }
        .formStyle(.grouped)
        .stateReadableWidth()
        .stateBackground()
        .navigationTitle(String(localized: "Enter address"))
        .stateInlineNavigationTitle()
        .safeAreaInset(edge: .bottom) {
            ConnectButton(model: model, draft: draft)
        }
        .onAppear {
            if draft.server.isEmpty { focusedField = .server }
        }
    }
}

// MARK: - Shared pieces

private struct DeviceIdentitySection: View {
    @Bindable var draft: ConnectDraft

    var body: some View {
        Section {
            TextField(String(localized: "Your name"), text: $draft.displayName)
                .textContentType(.name)
            TextField(String(localized: "Device name"), text: $draft.deviceName)
        } header: {
            Text("This device")
        } footer: {
            Text("Credentials stay in the Keychain. Your reminders stay on your server.")
        }
    }
}

private struct ConnectButton: View {
    @Bindable var model: AppModel
    @Bindable var draft: ConnectDraft
    @State private var isConnecting = false

    var body: some View {
        Button {
            connect()
        } label: {
            HStack(spacing: StateTheme.Space.inner) {
                if isConnecting {
                    ProgressView()
                        .controlSize(.small)
                        .tint(StateTheme.onAccent)
                }
                Text(isConnecting ? String(localized: "Connecting") : String(localized: "Connect"))
            }
        }
        .buttonStyle(.statePrimary)
        .disabled(!draft.canConnect || isConnecting)
        .keyboardShortcut(.defaultAction)
        #if os(macOS)
        .frame(width: 320)
        #else
        .frame(maxWidth: 440)
        #endif
        .padding(.horizontal, StateTheme.Space.section)
        .padding(.top, StateTheme.Space.group)
        .padding(.bottom, StateTheme.Space.inner)
        .frame(maxWidth: .infinity)
        .background(StateTheme.ground.opacity(0.94).ignoresSafeArea())
        .accessibilityIdentifier("connect")
    }

    private func connect() {
        guard draft.canConnect, let url = draft.serverURL else { return }
        isConnecting = true
        Task {
            await model.connect(
                serverURL: url,
                bootstrapToken: draft.method == .bootstrap ? draft.bootstrapToken : nil,
                pairingCode: draft.method == .pairingCode ? draft.pairingCode : nil,
                displayName: draft.displayName,
                deviceName: draft.deviceName,
                certificateFingerprint: draft.usesScannedServer ? draft.certificateFingerprint : nil,
                relayURL: draft.usesScannedServer ? draft.scannedRelayURL : nil
            )
            isConnecting = false
        }
    }
}

#if os(iOS)
struct PairingScannerView: UIViewControllerRepresentable {
    let onScan: (String) -> Void

    func makeCoordinator() -> Coordinator {
        Coordinator(onScan: onScan)
    }

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let controller = DataScannerViewController(
            recognizedDataTypes: [.barcode(symbologies: [.qr])],
            qualityLevel: .balanced,
            recognizesMultipleItems: false,
            isHighFrameRateTrackingEnabled: false,
            isPinchToZoomEnabled: true,
            isGuidanceEnabled: true,
            isHighlightingEnabled: true
        )
        controller.delegate = context.coordinator
        try? controller.startScanning()
        return controller
    }

    func updateUIViewController(_ uiViewController: DataScannerViewController, context: Context) {
        if !uiViewController.isScanning {
            try? uiViewController.startScanning()
        }
    }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        private let onScan: (String) -> Void
        private var completed = false

        init(onScan: @escaping (String) -> Void) {
            self.onScan = onScan
        }

        func dataScanner(
            _ dataScanner: DataScannerViewController,
            didAdd addedItems: [RecognizedItem],
            allItems: [RecognizedItem]
        ) {
            guard !completed else { return }
            for item in addedItems {
                guard case let .barcode(barcode) = item, let value = barcode.payloadStringValue else { continue }
                completed = true
                dataScanner.stopScanning()
                onScan(value)
                return
            }
        }
    }
}
#endif
