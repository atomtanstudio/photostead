// Behind your picture (and the backup pill): how the backup is going, the few settings there are,
// and which Photostead this phone is paired with.

import Photos
import SwiftUI

struct SettingsView: View {
    @Environment(Backup.self) private var backup
    @Environment(Library.self) private var library
    @Environment(\.openURL) private var openURL
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    @State private var confirmUnpair = false
    @State private var adultFailed: String?
    private let notices = Notices.shared

    var body: some View {
        @Bindable var backup = backup
        NavigationStack {
            List {
                Section {
                    Status().listRowInsets(EdgeInsets(top: 22, leading: 20, bottom: 22, trailing: 20))
                }
                if !backup.canRead {
                    Section {
                        if backup.access == .notDetermined {
                            Button("Allow access to Photos") { Task { await backup.requestAccess() } }
                        } else {
                            Text("Photostead can’t see your photos. Allow it in Settings → Photostead → Photos.")
                            Button("Open Settings") { openURL(URL(string: UIApplication.openSettingsURLString)!) }
                        }
                    }
                } else if backup.access == .limited {
                    Section {
                        Text("Photostead sees only the photos you chose. To back up everything, allow Full Access in Settings.")
                            .font(.callout).foregroundStyle(.secondary)
                        Button("Open Settings") { openURL(URL(string: UIApplication.openSettingsURLString)!) }
                    }
                }
                Section {
                    Toggle("Back up this iPhone", isOn: $backup.backupOn).accessibilityIdentifier("backup-on")
                    Toggle("Videos too", isOn: $backup.includeVideos).disabled(!backup.backupOn)
                    Toggle("Use cellular data", isOn: $backup.useCellular).disabled(!backup.backupOn)
                    // Turning it on asks iOS for notifications, the first time: here, when you tap for it.
                    Toggle("Notify me about backups", isOn: Binding(get: { notices.on }, set: { want in Task { await notices.turn(want) } }))
                        .disabled(!backup.backupOn)
                        .accessibilityIdentifier("notify")
                    if notices.on && notices.blocked {
                        Button("Allow notifications in Settings") { openURL(URL(string: UIApplication.openNotificationSettingsURLString)!) }
                    }
                } header: {
                    Text("Backing up")
                } footer: {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Notify me: when a backup finishes while you’re out of the app (once an hour at most), and when it can’t reach Photostead for a day.")
                        if backup.systemUploads {
                            Text("iOS also sends new photos and videos itself, in the background, as it does for iCloud Photos: no need to open Photostead.")
                        }
                    }
                }
                if library.me != nil {
                    Section {
                        Picker("Adult content", selection: Binding(get: { library.adult }, set: { mode in
                            adultFailed = nil
                            Task {
                                do { try await library.setAdult(mode) } catch { adultFailed = error.localizedDescription }
                            }
                        })) {
                            Text("Show").tag("show")
                            Text("Blur").tag("blur")
                            Text("Hide").tag("hide")
                        }
                        .accessibilityIdentifier("adult-mode")
                    } header: {
                        Text("Browsing")
                    } footer: {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("Nudity or sex, as Photostead found it or someone marked it. Blur: until you tap Show. Hide: left out of the library, albums and search. The same for you on the web.")
                            if let adultFailed { Text(adultFailed).foregroundStyle(.red) }
                        }
                    }
                }
                if let pairing = backup.pairing {
                    Section("Photostead") {
                        LabeledContent("Server", value: pairing.serverName)
                        LabeledContent("Account", value: pairing.account)
                        LabeledContent("This phone", value: pairing.deviceName)
                        if let url = backup.addressInUse {
                            LabeledContent("Reached at", value: displayAddress(url))
                        }
                        Button("Unpair this phone", role: .destructive) { confirmUnpair = true }
                    }
                }
                Section {
                    Link("Help and support", destination: Site.support)
                    Link("Privacy Policy", destination: Site.privacy)
                    LabeledContent("Version", value: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "")
                }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
            .refreshable { backup.refreshCounts(); backup.start() }
            .task { await notices.check() }
            .onChange(of: scenePhase) { _, now in if now == .active { Task { await notices.check() } } } // back from iOS's Settings
            .confirmationDialog("Unpair this phone?", isPresented: $confirmUnpair, titleVisibility: .visible) {
                Button("Unpair", role: .destructive) { backup.unpair() }
            } message: {
                Text("Nothing in Photostead is deleted. To back up again, pair again.")
            }
        }
    }
}

/// The pages on photostead.com the app links to: the App Store asks for a way to get help and for the
/// privacy policy, in the app itself.
enum Site {
    static let home = URL(string: "https://photostead.com")! // what Photostead is, and how to set one up
    static let support = URL(string: "https://photostead.com/support/")!
    static let privacy = URL(string: "https://photostead.com/privacy/")!
}

private struct Status: View {
    @Environment(Backup.self) private var backup

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .firstTextBaseline) {
                Text(backup.backedUp.formatted()).font(.system(size: 44, weight: .semibold, design: .rounded)).monospacedDigit()
                Text("of \(backup.total.formatted()) backed up").foregroundStyle(.secondary)
            }
            .accessibilityElement(children: .combine).accessibilityIdentifier("counts")
            ProgressView(value: backup.total == 0 ? 0 : Double(backup.backedUp), total: Double(max(backup.total, 1)))
                .tint(backup.remaining == 0 ? .green : .accentColor)
            HStack(spacing: 10) {
                Image(systemName: icon).foregroundStyle(tint)
                VStack(alignment: .leading, spacing: 2) {
                    Text(headline).font(.headline).accessibilityIdentifier("status")
                    if let detail { Text(detail).font(.callout).foregroundStyle(.secondary).lineLimit(3) }
                }
                Spacer()
            }
            if backup.pairing != nil && backup.canRead && backup.phase != .signedOut {
                Button {
                    backup.busy ? backup.stop() : backup.start()
                } label: {
                    Text(backup.busy ? "Pause" : "Back up now").frame(maxWidth: .infinity)
                        .foregroundStyle(Color.onAccent)
                }
                .buttonStyle(.borderedProminent).controlSize(.large)
            }
        }
    }

    private var sending: (count: Int, sent: Int64, size: Int64) {
        backup.inFlight.values.reduce((0, 0, 0)) { ($0.0 + 1, $0.1 + $1.sent, $0.2 + $1.size) }
    }

    private var headline: String {
        switch backup.phase {
        case .waitingForWiFi: return "Waiting for Wi-Fi"
        case .unreachable: return "Can’t reach Photostead"
        case .signedOut: return "Signed out"
        default: break
        }
        if sending.count > 0 { return sending.count == 1 ? "Sending 1" : "Sending \(sending.count)" }
        if let name = backup.preparingName, backup.preparing { return "Getting \(name) ready" }
        if backup.failed > 0 { return backup.failed == 1 ? "1 didn’t back up" : "\(backup.failed.formatted()) didn’t back up" }
        return backup.remaining == 0 && backup.total > 0 ? "Up to date" : backup.total == 0 ? "Nothing to back up yet" : "\(backup.remaining.formatted()) to back up"
    }

    private var detail: String? {
        if sending.count > 0, sending.size > 0 {
            let f = ByteCountFormatter.string(fromByteCount: sending.sent, countStyle: .file)
            let t = ByteCountFormatter.string(fromByteCount: sending.size, countStyle: .file)
            return "\(f) of \(t) · carries on if you leave the app"
        }
        if !backup.message.isEmpty { return backup.message }
        if let last = backup.lastRun { return "Last checked \(last.formatted(.relative(presentation: .named)))" }
        return nil
    }

    private var icon: String {
        switch backup.phase {
        case .waitingForWiFi: return "wifi"
        case .unreachable: return "exclamationmark.icloud"
        case .signedOut: return "person.crop.circle.badge.exclamationmark"
        default:
            if backup.busy { return "arrow.up.circle" }
            if backup.failed > 0 { return "exclamationmark.triangle.fill" }
            return backup.remaining == 0 && backup.total > 0 ? "checkmark.circle.fill" : "circle.dashed"
        }
    }

    private var tint: Color {
        switch backup.phase {
        case .unreachable, .signedOut: return .orange
        default:
            if backup.failed > 0 && !backup.busy { return .orange }
            return backup.remaining == 0 && backup.total > 0 ? .green : .secondary
        }
    }
}

extension Color {
    /// Text on the accent colour, which is near-black in light mode and near-white in dark: white, or
    /// black. A fixed colour, not the background style, which in a toolbar is the bar's light glass.
    static let onAccent = Color(uiColor: UIColor { $0.userInterfaceStyle == .dark ? .black : .white })
}

/// Your picture, top right on every tab: the settings.
struct AccountButton: View {
    @Environment(Backup.self) private var backup
    @State private var open = false

    var body: some View {
        Button { open = true } label: {
            Text(initials).font(.footnote.weight(.semibold)).foregroundStyle(Color.onAccent)
                .frame(width: 30, height: 30).background(Color.accentColor.gradient, in: Circle())
        }
        .accessibilityLabel("Settings")
        .accessibilityIdentifier("account")
        .sheet(isPresented: $open) { SettingsView() }
    }

    private var initials: String {
        let words = (backup.pairing?.account ?? "").split { $0 == " " || $0 == "@" || $0 == "." }
        return words.prefix(2).compactMap(\.first).map { String($0).uppercased() }.joined()
    }
}

/// Under the Library's title: how the backup's going, in a word. Tap for the details.
struct BackupPill: View {
    @Environment(Backup.self) private var backup
    @State private var open = false

    var body: some View {
        Button { open = true } label: {
            HStack(spacing: 6) {
                if backup.busy { ProgressView().controlSize(.mini) } else { Image(systemName: symbol).foregroundStyle(tint) }
                Text(text).lineLimit(2).multilineTextAlignment(.leading) // two lines at the largest text sizes, not cut off
            }
            .font(.footnote.weight(.medium))
            .padding(.horizontal, 12).padding(.vertical, 6)
            .background(.quaternary.opacity(0.6), in: Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("backup-pill")
        .sheet(isPresented: $open) { SettingsView() }
    }

    private var text: String {
        if !backup.backupOn { return "Backup is off" }
        if !backup.canRead { return "Allow Photos to back up" }
        switch backup.phase {
        case .waitingForWiFi: return "Backup waiting for Wi-Fi"
        case .unreachable: return "Backup can’t reach Photostead"
        case .signedOut: return "Signed out: pair again"
        default: break
        }
        if backup.busy || backup.remaining > 0 { return "Backing up · \(backup.remaining.formatted()) left" }
        if backup.failed > 0 { return "\(backup.failed.formatted()) didn’t back up" }
        return "Backed up"
    }

    private var symbol: String {
        if !backup.backupOn { return "pause.circle" }
        if !backup.canRead { return "exclamationmark.circle" }
        switch backup.phase {
        case .waitingForWiFi: return "wifi"
        case .unreachable, .signedOut: return "exclamationmark.circle"
        default: return backup.failed > 0 ? "exclamationmark.triangle" : "checkmark.circle.fill"
        }
    }

    private var tint: Color {
        if !backup.backupOn { return .secondary }
        if !backup.canRead { return .orange }
        switch backup.phase {
        case .unreachable, .signedOut: return .orange
        default: return backup.failed > 0 ? .orange : .green
        }
    }
}
