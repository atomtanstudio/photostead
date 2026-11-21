// Sharing from the phone (step 3 of docs/ios-plan.md): an album's people (household members, and
// anyone by name: they get a personal link, no account to make) and its share links (anyone with
// one sees the album: copy, send, its QR code, downloads and uploads, delete). And adding photos
// from this iPhone to an album, yours or one shared with you to add to.

import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

struct AlbumSharing: View {
    var album: AlbumRow
    @Environment(Library.self) private var library
    @Environment(\.dismiss) private var dismiss
    @State private var people: AlbumPeople?
    @State private var links: [ShareLink]?
    @State private var naming = false
    @State private var name = ""
    @State private var sending: [Any]?
    @State private var qr: ShareLink?
    @State private var problem = ""
    @State private var busy = false

    var body: some View {
        NavigationStack {
            List {
                Section {
                    if let people {
                        ForEach(people.members) { p in
                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(p.name)
                                    Text(p.guest == true ? "Guest · \(p.link.map { "open on \($0.devices) of \($0.maxDevices) devices" } ?? "personal link")" : "Household")
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer()
                                Menu(p.role == "contributor" ? "Can add" : "Can see") {
                                    Button("Can see") { act { try await library.api()?.share(album.id, with: p.id, role: "viewer") } }
                                    Button("Can add to it") { act { try await library.api()?.share(album.id, with: p.id, role: "contributor") } }
                                }
                                .font(.callout)
                                if let link = p.link {
                                    Button { sending = [message(link.url)] } label: { Image(systemName: "square.and.arrow.up") }
                                        .buttonStyle(.borderless)
                                        .accessibilityLabel("Send \(p.name) their link")
                                }
                            }
                            .swipeActions {
                                Button("Stop sharing", role: .destructive) { act { try await library.api()?.unshare(album.id, with: p.id) } }
                            }
                        }
                        ForEach(people.candidates) { p in
                            HStack {
                                Text(p.name).foregroundStyle(.secondary)
                                Spacer()
                                Button("Share") { act { try await library.api()?.share(album.id, with: p.id, role: "viewer") } }.buttonStyle(.borderless)
                            }
                        }
                    }
                    Button { name = ""; naming = true } label: { Label("Share with someone…", systemImage: "person.badge.plus") }
                        .accessibilityIdentifier("share-with-someone")
                } header: {
                    Text("People")
                } footer: {
                    Text("They see this album, not the rest of your library. Someone new gets a personal link: no account to make, on up to 3 devices.")
                }

                Section {
                    ForEach(links ?? []) { l in
                        VStack(alignment: .leading, spacing: 8) {
                            Text(l.url.replacingOccurrences(of: "https://", with: "").replacingOccurrences(of: "http://", with: ""))
                                .font(.callout.monospaced()).lineLimit(1).truncationMode(.middle)
                            Text([l.hasPassword ? "password" : nil, "\(l.views) \(l.views == 1 ? "view" : "views")", l.public ? nil : "home network only"]
                                .compactMap { $0 }.joined(separator: " · "))
                                .font(.caption).foregroundStyle(.secondary)
                            Toggle("Downloads", isOn: Binding(get: { l.allowDownload }, set: { v in act { _ = try await library.api()?.editLink(l, downloads: v, uploads: l.allowUpload) } }))
                            Toggle("Others can add photos", isOn: Binding(get: { l.allowUpload }, set: { v in act { _ = try await library.api()?.editLink(l, downloads: l.allowDownload, uploads: v) } }))
                            HStack(spacing: 18) {
                                Button { UIPasteboard.general.string = l.url } label: { Label("Copy", systemImage: "doc.on.doc") }
                                Button { sending = [message(l.url)] } label: { Label("Send", systemImage: "square.and.arrow.up") }
                                if l.qrImage != nil { Button { qr = l } label: { Label("QR", systemImage: "qrcode") }.accessibilityIdentifier("QR code") }
                                Spacer()
                                Button(role: .destructive) { act { try await library.api()?.deleteLink(l.id) } } label: { Image(systemName: "trash") }
                                    .accessibilityLabel("Delete the link")
                            }
                            .buttonStyle(.borderless)
                            .font(.callout)
                        }
                        .padding(.vertical, 4)
                    }
                    Button { act { _ = try await library.api()?.newLink(album.id) } } label: { Label(links?.isEmpty == false ? "Make another link" : "Make a link", systemImage: "link.badge.plus") }
                        .accessibilityIdentifier("make-link")
                } header: {
                    Text("Links")
                } footer: {
                    if links?.contains(where: { !$0.public }) == true {
                        Text("These links work on your home network and Tailscale. To send them to anyone, turn on Funnel in Photostead on the web, under Devices & remote access.")
                    } else {
                        Text("Anyone with a link sees this album, as it is now and as it changes. No account.")
                    }
                }
                if !problem.isEmpty { Section { Text(problem).foregroundStyle(.red) } }
            }
            .navigationTitle("Share “\(album.title)”")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
            .overlay { if busy { ProgressView() } }
            .task { await load() }
            .alert("Share with someone", isPresented: $naming) {
                TextField("Their name", text: $name).textInputAutocapitalization(.words)
                Button("Share") { Task { await shareNew() } }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("They get a personal link to open it, with no account to make.")
            }
            .sheet(isPresented: Binding(get: { sending != nil }, set: { if !$0 { sending = nil } })) {
                if let sending { ShareSheet(items: sending).ignoresSafeArea() }
            }
            .sheet(item: $qr) { l in QRCard(title: album.title, link: l) }
        }
    }

    private func message(_ url: String) -> String { "Here are the photos from “\(album.title)”: \(url)" }

    private func load() async {
        guard let api = library.api() else { return }
        do {
            async let p = api.albumPeople(album.id)
            async let l = api.albumLinks(album.id)
            people = try await p
            links = try await l
            problem = ""
        } catch {
            problem = error.localizedDescription
        }
    }

    private func act(_ change: @escaping () async throws -> Void) {
        Task {
            busy = true
            defer { busy = false }
            do {
                try await change()
                await load()
            } catch {
                problem = error.localizedDescription
            }
        }
    }

    /// Someone new: shared with by name, then their link to send them.
    private func shareNew() async {
        let n = name.trimmingCharacters(in: .whitespaces)
        guard !n.isEmpty, let api = library.api() else { return }
        busy = true
        defer { busy = false }
        do {
            let before = Set(people?.members.map(\.id) ?? [])
            let after = try await api.shareWithNew(album.id, name: n)
            people = after
            if let fresh = after.members.first(where: { !before.contains($0.id) }), let url = fresh.link?.url { sending = [message(url)] }
        } catch {
            problem = error.localizedDescription
        }
    }
}

/// A link's QR code, big, to show someone across the table (or screenshot for a card).
private struct QRCard: View {
    var title: String
    var link: ShareLink
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 18) {
            Text(title).font(.title2.weight(.bold)).multilineTextAlignment(.center)
            Text(link.allowUpload ? "Scan to see the photos, and add yours" : "Scan to see the photos").foregroundStyle(.secondary)
            if let image = link.qrImage {
                Image(uiImage: image).interpolation(.none).resizable().scaledToFit().frame(maxWidth: 280)
                    .padding(16).background(.white, in: RoundedRectangle(cornerRadius: 16))
            }
            Button("Done") { dismiss() }.buttonStyle(.borderedProminent)
        }
        .padding(28)
        .presentationDetents([.large])
    }
}

/// A file picked from the Photos library, as a file (videos aren't read into memory).
struct PickedFile: Transferable {
    var url: URL
    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(importedContentType: .item) { received in
            let dir = FileManager.default.temporaryDirectory.appending(path: "picked-\(UUID().uuidString)", directoryHint: .isDirectory)
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            let copy = dir.appending(path: received.file.lastPathComponent)
            try FileManager.default.copyItem(at: received.file, to: copy)
            return PickedFile(url: copy)
        }
    }
}

/// Adding photos from this iPhone to an album: picked, then sent one by one.
struct AddFromPhone: ViewModifier {
    var album: AlbumRow
    @Binding var picking: Bool
    var done: () -> Void
    @Environment(Library.self) private var library
    @State private var picked: [PhotosPickerItem] = []
    @State private var note = ""

    func body(content: Content) -> some View {
        content
            .photosPicker(isPresented: $picking, selection: $picked, matching: .any(of: [.images, .videos]), photoLibrary: .shared())
            .onChange(of: picked) {
                guard !picked.isEmpty else { return }
                let items = picked
                picked = []
                Task { await send(items) }
            }
            .overlay(alignment: .bottom) {
                if !note.isEmpty {
                    Text(note).font(.subheadline.weight(.medium)).foregroundStyle(.white)
                        .padding(.horizontal, 16).padding(.vertical, 10).background(.black.opacity(0.75), in: Capsule()).padding(.bottom, 24)
                        .accessibilityIdentifier("add-note")
                }
            }
    }

    private func send(_ items: [PhotosPickerItem]) async {
        guard let api = library.api() else { return }
        var sent = 0
        for (i, item) in items.enumerated() {
            note = "Adding \(i + 1) of \(items.count)…"
            do {
                guard let file = try await item.loadTransferable(type: PickedFile.self) else { continue }
                defer { try? FileManager.default.removeItem(at: file.url.deletingLastPathComponent()) }
                try await api.upload(file.url, named: file.url.lastPathComponent, toAlbum: album.id)
                sent += 1
            } catch {
                note = "Couldn’t add one: \(error.localizedDescription)"
                try? await Task.sleep(for: .seconds(2))
            }
        }
        note = sent == items.count ? "Added \(sent == 1 ? "1 item" : "\(sent) items"); they show once Photostead has them ready." : "Added \(sent) of \(items.count)."
        done()
        try? await Task.sleep(for: .seconds(2.5))
        note = ""
    }
}
