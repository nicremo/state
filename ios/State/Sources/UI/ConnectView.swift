import SwiftUI
import UIKit
import VisionKit

/// The screen that turns a running server into a paired iPhone. It leads with
/// the documentation, because everything below it assumes a server exists, and
/// it names which of the two secrets is needed instead of showing both at once.
struct ConnectView: View {
    @Bindable var model: AppModel

    private enum Method: Hashable {
        case pairingCode
        case bootstrap
    }

    @State private var method: Method = .bootstrap
    @State private var server = ""
    @State private var bootstrapToken = ""
    @State private var pairingCode = ""
    @State private var certificateFingerprint: String?
    @State private var scannedServer = ""
    @State private var displayName = ""
    @State private var deviceName = UIDevice.current.name
    @State private var scansCode = false
    @State private var isConnecting = false
    @FocusState private var focusedField: Field?

    private enum Field: Hashable {
        case server
        case secret
        case name
        case device
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: StateTheme.Space.section) {
                    masthead
                    documentationCard
                    form
                    actions
                    demoEntry
                    reassurance
                }
                .frame(maxWidth: 560)
                .frame(maxWidth: .infinity)
                .padding(.horizontal, StateTheme.Space.section)
                .padding(.top, StateTheme.Space.stage)
                .padding(.bottom, StateTheme.Space.stage + StateTheme.Space.section)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(StateTheme.warmBackground.ignoresSafeArea())
            .navigationBarHidden(true)
            .sheet(isPresented: $scansCode) { scanner }
        }
    }

    // MARK: Sections

    private var masthead: some View {
        HStack(alignment: .center, spacing: StateTheme.Space.block) {
            StateMark(size: 62)

            VStack(alignment: .leading, spacing: StateTheme.Space.tight) {
                Text(verbatim: "State")
                    .font(.title.bold())
                    .foregroundStyle(StateTheme.graphite)
                Text("Connect this iPhone to your own server.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var documentationCard: some View {
        NavigationLink {
            DocumentationView()
        } label: {
            HStack(spacing: StateTheme.Space.group) {
                Image(systemName: "book")
                    .font(.system(size: 17, weight: .medium))
                    .foregroundStyle(StateTheme.accent)
                    .frame(width: 42, height: 42)
                    .background(StateTheme.accentSoft, in: RoundedRectangle(cornerRadius: 12, style: .continuous))

                VStack(alignment: .leading, spacing: StateTheme.Space.hairline) {
                    Text("No server yet? Start here")
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(StateTheme.graphite)
                    Text("Set up the server, pair this iPhone, connect your agents.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.leading)
                }

                Spacer(minLength: StateTheme.Space.tight)

                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.tertiary)
            }
            .padding(StateTheme.Space.block)
            .background(
                RoundedRectangle(cornerRadius: 16, style: .continuous)
                    .fill(StateTheme.accentSoft.opacity(0.7))
            )
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("open-documentation")
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.block) {
            FieldGroup(label: String(localized: "Server address")) {
                TextField(String(localized: "Server URL"), text: $server, prompt: Text(verbatim: "https://state.example.com"))
                    .textContentType(.URL)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .submitLabel(.next)
                    .focused($focusedField, equals: .server)
                    .onSubmit { focusedField = .secret }
            }

            VStack(alignment: .leading, spacing: StateTheme.Space.inner) {
                Picker(String(localized: "How to connect"), selection: $method.animation(StateTheme.stateChange)) {
                    Text("First setup").tag(Method.bootstrap)
                    Text("Pairing code").tag(Method.pairingCode)
                }
                .pickerStyle(.segmented)

                switch method {
                case .bootstrap:
                    FieldGroup(label: String(localized: "Bootstrap token")) {
                        SecureField(String(localized: "Bootstrap token"), text: $bootstrapToken)
                            .focused($focusedField, equals: .secret)
                    }
                    helper(String(localized: "Printed once by your server with state-server bootstrap-token. It makes this iPhone the owner."))
                case .pairingCode:
                    FieldGroup(label: String(localized: "One-time pairing code")) {
                        SecureField(String(localized: "One-time pairing code"), text: $pairingCode)
                            .textContentType(.oneTimeCode)
                            .focused($focusedField, equals: .secret)
                    }
                    helper(String(localized: "Created in State on an already paired device, under Settings."))
                }
            }

            FieldGroup(label: String(localized: "Your name")) {
                TextField(String(localized: "Your name"), text: $displayName)
                    .textContentType(.name)
                    .submitLabel(.next)
                    .focused($focusedField, equals: .name)
                    .onSubmit { focusedField = .device }
            }

            FieldGroup(label: String(localized: "Device name")) {
                TextField(String(localized: "Device name"), text: $deviceName)
                    .submitLabel(.done)
                    .focused($focusedField, equals: .device)
                    .onSubmit { connect() }
            }
        }
    }

    private var actions: some View {
        VStack(spacing: StateTheme.Space.group) {
            Button {
                connect()
            } label: {
                HStack(spacing: StateTheme.Space.inner) {
                    if isConnecting {
                        ProgressView()
                            .controlSize(.small)
                            .tint(.white)
                    }
                    Text("Connect")
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, StateTheme.Space.snug)
            }
            .buttonStyle(.borderedProminent)
            .disabled(!canConnect || isConnecting)
            .animation(StateTheme.stateChange, value: canConnect)

            if DataScannerViewController.isSupported {
                Button {
                    scansCode = true
                } label: {
                    Label(String(localized: "Scan pairing QR code"), systemImage: "qrcode.viewfinder")
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, StateTheme.Space.hairline)
                }
                .buttonStyle(.bordered)
            }
        }
    }

    private var demoEntry: some View {
        VStack(spacing: StateTheme.Space.snug) {
            Divider()
            Button {
                Task { await model.enterDemo() }
            } label: {
                Text("Look around without a server")
                    .font(.subheadline)
            }
            .buttonStyle(.plain)
            .foregroundStyle(StateTheme.accent)
            .accessibilityIdentifier("explore-demo")
            .padding(.top, StateTheme.Space.tight)
        }
    }

    private var reassurance: some View {
        Label(
            String(localized: "Credentials stay in the iOS Keychain. Reminder data remains on your own server."),
            systemImage: "lock.shield"
        )
        .labelStyle(.tight)
        .font(.footnote)
        .foregroundStyle(.secondary)
    }

    private var scanner: some View {
        NavigationStack {
            PairingScannerView { value in
                applyScanned(value)
                scansCode = false
            }
            .ignoresSafeArea(edges: .bottom)
            .navigationTitle("Scan pairing code")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { scansCode = false }
                }
            }
        }
    }

    private func helper(_ text: String) -> some View {
        Text(text)
            .font(.caption)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }

    // MARK: Behavior

    private var canConnect: Bool {
        guard let url = URL(string: server.trimmingCharacters(in: .whitespacesAndNewlines)), url.scheme != nil else {
            return false
        }
        let secret = method == .bootstrap ? bootstrapToken : pairingCode
        return !displayName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !deviceName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !secret.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func connect() {
        guard canConnect, let url = URL(string: server.trimmingCharacters(in: .whitespacesAndNewlines)) else { return }
        focusedField = nil
        isConnecting = true
        Task {
            await model.connect(
                serverURL: url,
                bootstrapToken: method == .bootstrap ? bootstrapToken : nil,
                pairingCode: method == .pairingCode ? pairingCode : nil,
                displayName: displayName,
                deviceName: deviceName,
                certificateFingerprint: server.trimmingCharacters(in: .whitespacesAndNewlines) == scannedServer ? certificateFingerprint : nil
            )
            isConnecting = false
        }
    }

    private func applyScanned(_ value: String) {
        guard let payload = PairingPayload(value: value) else {
            model.presentedError = String(localized: "This is not a valid State pairing code.")
            return
        }
        server = payload.serverURL.absoluteString
        scannedServer = server
        certificateFingerprint = payload.certificateFingerprint
        if let token = payload.bootstrapToken, !token.isEmpty {
            bootstrapToken = token
            method = .bootstrap
        }
        if let code = payload.pairingCode, !code.isEmpty {
            pairingCode = code
            method = .pairingCode
        }
    }
}

/// A labelled input. The label above the field survives long localized strings
/// that a leading label would squeeze.
private struct FieldGroup<Content: View>: View {
    let label: String
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.snug) {
            Text(label)
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            content
                .textFieldStyle(.plain)
                .padding(.horizontal, StateTheme.Space.group)
                .padding(.vertical, StateTheme.Space.group)
                .background(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .fill(Color(uiColor: .secondarySystemGroupedBackground))
                )
        }
    }
}

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
