// Pairing with Photostead: scan the QR code it shows (Devices & remote access → Show a pairing code),
// open its link from the Camera app, or type the address and code. A link from anywhere else is
// shown first, with where it would send your photos, before anything happens. It's the first screen
// anyone sees (App Review too), so it says what Photostead is, and where to get one.

import AVFoundation
import SwiftUI
import VisionKit

struct PairView: View {
    @Environment(Backup.self) private var backup
    @State private var address = ""
    @State private var code = ""
    @State private var name = UIDevice.current.name
    @State private var scanning = false
    @State private var scanned: PairingLink? // confirmed once the camera's sheet has gone
    @State private var typing = false // the address and code, typed: the way without the camera
    @State private var busy = false
    @State private var error = ""

    var body: some View {
        ScrollView {
            VStack(spacing: 24) {
                Brand().padding(.top, 20).padding(.bottom, 4)
                pairing
                newHere
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: 24) { links }
                    VStack(spacing: 12) { links }
                }
                .font(.footnote)
                .tint(.secondary)
            }
            .padding(.horizontal, 20).padding(.vertical, 24)
            .frame(maxWidth: 560)
            .frame(maxWidth: .infinity)
        }
        .scrollDismissesKeyboard(.interactively)
        .background(Color(.systemGroupedBackground))
        .sheet(isPresented: $scanning, onDismiss: {
            if let link = scanned { backup.incoming = link }
            scanned = nil
        }) {
            ScanSheet { text in
                if let link = PairingLink(text), !link.urls.isEmpty {
                    scanned = link
                } else {
                    error = "That isn't Photostead’s pairing code."
                }
                scanning = false
            } typeInstead: {
                scanning = false
                typing = true
            }
        }
        .sheet(item: Binding(get: { backup.incoming.map(Incoming.init) }, set: { if $0 == nil { backup.incoming = nil } })) { item in
            ConfirmPairing(link: item.link, name: $name) { await pair(link: item.link) }
                .presentationDetents([.medium, .large])
        }
    }

    /// Scan first (it carries every address Photostead has); typing for when there's no camera.
    private var pairing: some View {
        Card {
            Text("Pair this iPhone").font(.title3.weight(.semibold)).accessibilityAddTraits(.isHeader)
            Text("On your computer, open Photostead, then **Devices & remote access → Show a pairing code**.")
                .foregroundStyle(.secondary)
            Button {
                error = ""
                scanning = true
            } label: {
                Label("Scan the pairing code", systemImage: "qrcode.viewfinder")
                    .frame(maxWidth: .infinity)
                    .foregroundStyle(Color.onAccent)
            }
            .buttonStyle(.borderedProminent).controlSize(.large)
            .accessibilityIdentifier("scan")
            if typing {
                typed
            } else {
                Button {
                    withAnimation(.snappy) { typing = true }
                } label: {
                    Text("Type the address and code instead").frame(maxWidth: .infinity)
                }
                .controlSize(.large)
                .accessibilityIdentifier("type-instead")
            }
            if !error.isEmpty {
                Label(error, systemImage: "exclamationmark.triangle.fill").font(.callout).foregroundStyle(.red)
            }
        }
    }

    @ViewBuilder private var typed: some View {
        Divider().padding(.vertical, 4)
        VStack(alignment: .leading, spacing: 6) {
            Field(title: "Photostead’s address", text: $address, prompt: "photostead.example.ts.net")
                .textContentType(.URL).keyboardType(.URL).accessibilityIdentifier("address")
            Text("Its tailnet name, or at home its address on your network, like 192.168.1.10:4800.")
                .font(.footnote).foregroundStyle(.secondary)
        }
        Field(title: "Pairing code", text: $code, prompt: "8 letters and numbers")
            .submitLabel(.go).onSubmit(pairTyped)
            .accessibilityIdentifier("code")
        Field(title: "This phone is called", text: $name, prompt: "iPhone")
        Button(action: pairTyped) {
            Text(busy ? "Pairing…" : "Pair").frame(maxWidth: .infinity)
        }
        .buttonStyle(.bordered).controlSize(.large)
        .disabled(busy || address.isEmpty || code.count < 8)
        .accessibilityIdentifier("pair")
    }

    /// For anyone who found the app before the server: what it pairs with, and where that comes from.
    private var newHere: some View {
        Card {
            Text("Don’t have Photostead yet?").font(.headline).accessibilityAddTraits(.isHeader)
            Text("Photostead runs on a computer or home server of your own, and keeps your library there. This app backs your iPhone up to it.")
                .foregroundStyle(.secondary)
            Link(destination: Site.home) {
                HStack(spacing: 6) {
                    Text("Set one up at photostead.com")
                    Image(systemName: "arrow.up.right").font(.footnote.weight(.semibold)).accessibilityHidden(true)
                }
                .fontWeight(.medium)
            }
        }
    }

    @ViewBuilder private var links: some View {
        Link("Help and support", destination: Site.support)
        Link("Privacy Policy", destination: Site.privacy)
    }

    private func pairTyped() {
        guard !busy, !address.isEmpty, code.count >= 8 else { return }
        Task { await pair(link: PairingLink(code)) }
    }

    private func pair(link: PairingLink?) async {
        guard let link else { return }
        let urls = link.urls.isEmpty ? [serverURL(address)].compactMap { $0 } : link.urls
        guard !urls.isEmpty else {
            error = "Type Photostead’s address."
            return
        }
        busy = true
        error = ""
        defer { busy = false }
        do {
            try await backup.pair(urls: urls, code: link.code, name: name.isEmpty ? "iPhone" : name)
        } catch {
            self.error = error.localizedDescription
            backup.incoming = nil
        }
    }
}

/// The mark as the app icon draws it, with the name and what it is.
private struct Brand: View {
    var body: some View {
        VStack(spacing: 18) {
            Mark()
                .foregroundStyle(Color(red: 0.957, green: 0.957, blue: 0.949))
                .frame(width: 92, height: 92)
                .background(Color(white: 0.043), in: RoundedRectangle(cornerRadius: 21, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 21, style: .continuous).strokeBorder(.white.opacity(0.14), lineWidth: 0.5)) // its edge, on black
                .accessibilityHidden(true)
            VStack(spacing: 8) {
                Text("Photostead").font(.largeTitle.weight(.bold)).accessibilityAddTraits(.isHeader)
                Text("Your photos and videos, on your own server, backed up from this iPhone.")
                    .font(.title3).foregroundStyle(.secondary)
            }
            .multilineTextAlignment(.center)
        }
    }
}

/// A rounded card on the grouped background, as Settings' sections are.
private struct Card<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 14) { content }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 26, style: .continuous))
    }
}

private struct Incoming: Identifiable {
    var link: PairingLink
    var id: String { link.code }
}

/// A pairing link, before it's used: where photos would go.
private struct ConfirmPairing: View {
    var link: PairingLink
    @Binding var name: String
    var pair: () async -> Void
    @State private var busy = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                Text("Pair with this Photostead?").font(.title2.weight(.semibold))
                Text("Your photos and videos will be backed up to it.").foregroundStyle(.secondary)
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(link.urls, id: \.self) { url in
                        Label(displayAddress(url), systemImage: url.scheme == "https" ? "lock.fill" : "house")
                            .font(.callout.monospaced())
                    }
                }
                Field(title: "This phone is called", text: $name, prompt: "iPhone")
                Button {
                    busy = true
                    Task {
                        await pair()
                        busy = false
                    }
                } label: {
                    Text(busy ? "Pairing…" : "Pair").frame(maxWidth: .infinity).foregroundStyle(Color.onAccent)
                }
                .buttonStyle(.borderedProminent).controlSize(.large).disabled(busy)
                .accessibilityIdentifier("confirm-pair")
            }
            .padding(24)
        }
        .scrollBounceBehavior(.basedOnSize)
    }
}

struct Field: View {
    var title: String
    @Binding var text: String
    var prompt: String
    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title).font(.subheadline).foregroundStyle(.secondary)
            // The placeholder is drawn here, not by the text field, which shrinks its own to fit the
            // width: at the largest text sizes it was tiny. This one wraps, at full size, and the
            // field grows with it (no fixed height).
            ZStack(alignment: .topLeading) {
                if text.isEmpty {
                    // An address breaks after its dots, not with a hyphen someone might type.
                    Text(prompt.replacingOccurrences(of: ".", with: ".\u{200B}"))
                        .foregroundStyle(Color(.placeholderText)).accessibilityHidden(true)
                        .onTapGesture { focused = true } // its second line, below the field's own
                }
                TextField(title, text: $text, prompt: Text(""))
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .focused($focused)
                    .accessibilityHint(prompt)
            }
            .padding(.horizontal, 14).padding(.vertical, 12)
            .frame(minHeight: 48)
            .background(Color(.tertiarySystemFill), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
    }
}

/// The camera, reading the QR code, with a way out and a way round: Cancel, and typing it instead
/// (no camera that reads codes, as in the Simulator, or the camera not allowed).
private struct ScanSheet: View {
    var found: (String) -> Void
    var typeInstead: () -> Void
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @State private var camera = AVCaptureDevice.authorizationStatus(for: .video)

    var body: some View {
        NavigationStack {
            Group {
                if !DataScannerViewController.isSupported {
                    ContentUnavailableView {
                        Label("This iPhone can’t scan codes", systemImage: "camera")
                    } description: {
                        Text("Type Photostead’s address and code instead.")
                    } actions: {
                        Button("Type it instead", action: typeInstead).buttonStyle(.bordered)
                    }
                } else if camera == .denied || camera == .restricted {
                    ContentUnavailableView {
                        Label("Photostead can’t use the camera", systemImage: "camera")
                    } description: {
                        Text("Allow it in Settings → Photostead → Camera, or type the address and code instead.")
                    } actions: {
                        Button("Open Settings") { openURL(URL(string: UIApplication.openSettingsURLString)!) }.buttonStyle(.bordered)
                        Button("Type it instead", action: typeInstead)
                    }
                } else if camera == .authorized {
                    Scanner(found: found)
                        .ignoresSafeArea()
                        .overlay(alignment: .bottom) {
                            Text("Point the camera at the code on Photostead’s Devices & remote access page.")
                                .font(.callout).multilineTextAlignment(.center)
                                .padding(.horizontal, 18).padding(.vertical, 12)
                                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                                .padding(20)
                        }
                } else {
                    Color.clear // iOS is asking for the camera
                }
            }
            .navigationTitle("Scan the pairing code")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } } }
            .task {
                // Asked here, when you tapped Scan, not before.
                if camera == .notDetermined, DataScannerViewController.isSupported {
                    camera = await AVCaptureDevice.requestAccess(for: .video) ? .authorized : .denied
                }
            }
        }
    }
}

/// The camera, reading QR codes.
private struct Scanner: UIViewControllerRepresentable {
    var found: (String) -> Void

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(recognizedDataTypes: [.barcode(symbologies: [.qr])], isHighlightingEnabled: true)
        scanner.delegate = context.coordinator
        try? scanner.startScanning()
        return scanner
    }

    func updateUIViewController(_ controller: DataScannerViewController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(found: found) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let found: (String) -> Void
        var done = false
        init(found: @escaping (String) -> Void) { self.found = found }

        func dataScanner(_ scanner: DataScannerViewController, didAdd items: [RecognizedItem], allItems: [RecognizedItem]) {
            for case .barcode(let code) in items {
                if let text = code.payloadStringValue, !done {
                    done = true
                    found(text)
                }
            }
        }
    }
}

/// Photostead's mark: a house with a lens in it.
struct Mark: View {
    var body: some View {
        Canvas { ctx, size in
            let s = min(size.width, size.height) / 24
            func p(_ x: Double, _ y: Double) -> CGPoint { CGPoint(x: x * s, y: y * s) }
            var house = Path()
            house.move(to: p(3.5, 10.6))
            house.addLine(to: p(12, 3.6))
            house.addLine(to: p(20.5, 10.6))
            house.addLine(to: p(20.5, 19.7))
            house.addQuadCurve(to: p(19.7, 20.5), control: p(20.5, 20.5))
            house.addLine(to: p(4.3, 20.5))
            house.addQuadCurve(to: p(3.5, 19.7), control: p(3.5, 20.5))
            house.closeSubpath()
            let style = StrokeStyle(lineWidth: 1.7 * s, lineCap: .round, lineJoin: .round)
            ctx.stroke(house, with: .foreground, style: style)
            ctx.stroke(Path(ellipseIn: CGRect(x: (12 - 3.3) * s, y: (14.2 - 3.3) * s, width: 6.6 * s, height: 6.6 * s)), with: .foreground, style: style)
        }
    }
}
