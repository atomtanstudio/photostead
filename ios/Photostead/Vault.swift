// The vault: items kept out of everything else (the timeline, albums, search, sharing), behind a
// PIN, as on the web. On the phone the PIN can be kept in the Keychain behind Face ID, so the
// vault opens with a glance. It locks again when you leave it, and its pictures are never kept on
// the phone (Photostead sends them no-store).

import LocalAuthentication
import SwiftUI

struct VaultView: View {
    @Environment(Library.self) private var library
    @State private var state: VaultState?
    @State private var pin = ""
    @State private var again = ""
    @State private var problem = ""
    @State private var busy = false
    @State private var offerFaceID = false

    var body: some View {
        Group {
            if let state, state.unlocked {
                RemoteGrid(title: "Vault", collection: "vault")
            } else if let state, !state.hasPin {
                form(title: "Set a PIN for the vault", detail: "4 to 12 digits. Items in the vault show only here, after the PIN (or Face ID).") {
                    SecureField("PIN", text: $pin).keyboardType(.numberPad).textContentType(.newPassword).accessibilityIdentifier("vault-pin")
                    SecureField("The PIN again", text: $again).keyboardType(.numberPad).textContentType(.newPassword).accessibilityIdentifier("vault-pin-again")
                    Button("Set the PIN") { Task { await setPIN() } }
                        .disabled(!valid(pin) || pin != again || busy)
                        .accessibilityIdentifier("vault-set")
                }
            } else if state != nil {
                form(title: "The vault is locked", detail: waitNote) {
                    if VaultKey.saved {
                        Button { Task { await faceID() } } label: { Label("Open with Face ID", systemImage: "faceid") }
                    }
                    SecureField("PIN", text: $pin).keyboardType(.numberPad).accessibilityIdentifier("vault-pin")
                    Button("Open") { Task { await unlock(pin) } }.disabled(!valid(pin) || busy).accessibilityIdentifier("vault-open")
                }
            } else {
                ProgressView()
            }
        }
        .navigationTitle("Vault")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            await load()
            if state?.unlocked == false, state?.hasPin == true, VaultKey.saved { await faceID() }
        }
        .onDisappear { Task { try? await library.api()?.lockVault() } }
        .alert("Open the vault with Face ID next time?", isPresented: $offerFaceID) {
            Button("Use Face ID") { _ = VaultKey.save(pin); pin = "" }
            Button("Not now", role: .cancel) { pin = "" }
        } message: {
            Text("The PIN is kept on this iPhone, behind Face ID.")
        }
    }

    private func form(title: String, detail: String, @ViewBuilder fields: () -> some View) -> some View {
        Form {
            Section {
                VStack(spacing: 10) {
                    Image(systemName: "lock.fill").font(.largeTitle).foregroundStyle(.secondary)
                    Text(title).font(.headline)
                    if !detail.isEmpty { Text(detail).font(.footnote).foregroundStyle(.secondary).multilineTextAlignment(.center) }
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 8)
            }
            Section { fields() } footer: {
                if !problem.isEmpty { Text(problem).foregroundStyle(.red).accessibilityIdentifier("vault-problem") }
            }
        }
    }

    private var waitNote: String {
        guard let until = state?.waitUntil, until > Date() else { return "" }
        return "Too many wrong PINs: try again \(until.formatted(.relative(presentation: .named)))."
    }

    private func valid(_ p: String) -> Bool { (4...12).contains(p.count) && p.allSatisfy(\.isNumber) }

    private func load() async {
        do { state = try await library.api()?.vault() } catch { problem = error.localizedDescription }
    }

    private func setPIN() async {
        busy = true
        defer { busy = false }
        do {
            _ = try await library.api()?.setVaultPIN(pin)
            again = ""
            await unlock(pin)
        } catch {
            problem = error.localizedDescription
        }
    }

    private func unlock(_ typed: String, fromFaceID: Bool = false) async {
        busy = true
        defer { busy = false }
        do {
            state = try await library.api()?.unlockVault(pin: typed)
            problem = ""
            if !fromFaceID, !VaultKey.saved, VaultKey.canUse { offerFaceID = true } else { pin = "" }
        } catch PhotosteadError.refused(let status, let detail) {
            if fromFaceID, status == 403 || status == 401 { VaultKey.delete() } // the PIN changed since it was kept
            problem = detail.isEmpty ? "That PIN isn't right." : detail
            await load()
        } catch {
            problem = error.localizedDescription
        }
    }

    private func faceID() async {
        guard let kept = await VaultKey.read() else { return }
        await unlock(kept, fromFaceID: true)
    }
}

/// The vault's PIN on this iPhone, behind Face ID (or Touch ID): read only after a glance.
enum VaultKey {
    private static let service = "studio.atomtan.photostead.vault"
    private static let account = "pin"

    static var canUse: Bool { LAContext().canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: nil) }

    /// Whether a PIN is kept (asked without Face ID: an item behind it says so by refusing).
    static var saved: Bool {
        let context = LAContext()
        context.interactionNotAllowed = true
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account,
                                    kSecUseAuthenticationContext as String: context]
        let status = SecItemCopyMatching(query as CFDictionary, nil)
        return status == errSecSuccess || status == errSecInteractionNotAllowed
    }

    static func save(_ pin: String) -> Bool {
        guard let access = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleWhenPasscodeSetThisDeviceOnly, .biometryCurrentSet, nil) else { return false }
        delete()
        let add: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account,
                                  kSecValueData as String: Data(pin.utf8), kSecAttrAccessControl as String: access]
        return SecItemAdd(add as CFDictionary, nil) == errSecSuccess
    }

    /// The PIN, once Face ID says it's you (nil if not, or none is kept).
    static func read() async -> String? {
        await Task.detached {
            let context = LAContext()
            context.localizedReason = "Open the vault"
            var item: CFTypeRef?
            let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account,
                                        kSecReturnData as String: true, kSecUseAuthenticationContext as String: context]
            guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess, let data = item as? Data else { return nil }
            return String(data: data, encoding: .utf8)
        }.value
    }

    static func delete() {
        SecItemDelete([kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account] as CFDictionary)
    }
}

/// Moving items to the vault before it has a PIN: one set here first.
struct NewVaultPIN: View {
    var then: () -> Void
    @Environment(Library.self) private var library
    @Environment(\.dismiss) private var dismiss
    @State private var pin = ""
    @State private var again = ""
    @State private var problem = ""

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    SecureField("PIN", text: $pin).keyboardType(.numberPad).textContentType(.newPassword).accessibilityIdentifier("vault-pin")
                    SecureField("The PIN again", text: $again).keyboardType(.numberPad).textContentType(.newPassword).accessibilityIdentifier("vault-pin-again")
                } header: {
                    Text("A PIN for the vault")
                } footer: {
                    Text(problem.isEmpty ? "4 to 12 digits. Items in the vault show only there, after the PIN (or Face ID)." : problem)
                        .foregroundStyle(problem.isEmpty ? Color.secondary : Color.red)
                }
            }
            .navigationTitle("The vault")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Set it") { Task { await set() } }
                        .disabled(!(4...12).contains(pin.count) || !pin.allSatisfy(\.isNumber) || pin != again)
                        .accessibilityIdentifier("vault-set")
                }
            }
        }
    }

    private func set() async {
        do {
            _ = try await library.api()?.setVaultPIN(pin)
            dismiss()
            then()
        } catch {
            problem = error.localizedDescription
        }
    }
}
