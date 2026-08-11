import SwiftUI
import VisionKit

struct OnboardingView: View {
    @Bindable var model: AppModel
    @State private var server = ""
    @State private var bootstrapToken = ""
    @State private var pairingCode = ""
    @State private var displayName = "Fabian"
    @State private var deviceName = UIDevice.current.name
    @State private var scansCode = false
    @State private var isConnecting = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 28) {
                    VStack(alignment: .leading, spacing: 10) {
                        Image(systemName: "point.3.connected.trianglepath.dotted")
                            .font(.system(size: 42, weight: .semibold))
                            .foregroundStyle(StateTheme.accent)
                            .accessibilityHidden(true)
                        Text("State")
                            .font(.largeTitle.bold())
                        Text("Your agents remember. You stay in control.")
                            .font(.title3)
                            .foregroundStyle(.secondary)
                    }

                    VStack(alignment: .leading, spacing: 14) {
                        Label(String(localized: "Connect your server"), systemImage: "server.rack")
                            .font(.headline)
                        TextField(String(localized: "Server URL"), text: $server, prompt: Text("https://state.example.com"))
                            .textContentType(.URL)
                            .keyboardType(.URL)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .textFieldStyle(.roundedBorder)

                        SecureField(String(localized: "One-time pairing code"), text: $pairingCode)
                            .textContentType(.oneTimeCode)
                            .textFieldStyle(.roundedBorder)

                        DisclosureGroup(String(localized: "First owner setup")) {
                            SecureField(String(localized: "Bootstrap token"), text: $bootstrapToken)
                                .textFieldStyle(.roundedBorder)
                                .padding(.top, 10)
                        }

                        TextField(String(localized: "Your name"), text: $displayName)
                            .textContentType(.name)
                            .textFieldStyle(.roundedBorder)
                        TextField(String(localized: "Device name"), text: $deviceName)
                            .textFieldStyle(.roundedBorder)
                    }

                    VStack(spacing: 12) {
                        Button {
                            connect()
                        } label: {
                            HStack {
                                if isConnecting { ProgressView().tint(.white) }
                                Text("Connect")
                                    .frame(maxWidth: .infinity)
                            }
                            .padding(.vertical, 8)
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(!canConnect || isConnecting)

                        Button {
                            Task { await model.enterDemo() }
                        } label: {
                            Label(String(localized: "Explore demo"), systemImage: "sparkles")
                                .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.bordered)
                        .accessibilityIdentifier("explore-demo")

                        if DataScannerViewController.isSupported {
                            Button {
                                scansCode = true
                            } label: {
                                Label(String(localized: "Scan pairing QR code"), systemImage: "qrcode.viewfinder")
                                    .frame(maxWidth: .infinity)
                            }
                            .buttonStyle(.bordered)
                        }
                    }

                    Label(
                        String(localized: "Credentials stay in the iOS Keychain. Reminder data remains on your own server."),
                        systemImage: "lock.shield"
                    )
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                }
                .padding(24)
                .frame(maxWidth: 620)
                .frame(maxWidth: .infinity)
            }
            .background(StateTheme.warmBackground)
            .navigationBarHidden(true)
            .sheet(isPresented: $scansCode) {
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
        }
    }

    private var canConnect: Bool {
        URL(string: server) != nil
            && !displayName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !deviceName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && (!pairingCode.isEmpty || !bootstrapToken.isEmpty)
    }

    private func connect() {
        guard let url = URL(string: server) else { return }
        isConnecting = true
        Task {
            await model.connect(
                serverURL: url,
                bootstrapToken: bootstrapToken,
                pairingCode: pairingCode,
                displayName: displayName,
                deviceName: deviceName
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
        bootstrapToken = payload.bootstrapToken ?? ""
        pairingCode = payload.pairingCode ?? ""
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
