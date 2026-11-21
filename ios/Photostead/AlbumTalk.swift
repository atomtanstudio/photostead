// Talking about a shared album, and picking from it (proofing), as the web has it (web/src/Talk.tsx):
// the album's activity (who added what, and what people said) with a box to say something, and its
// picks: everyone's for the album's owner, your own for anyone else. In the viewer, an item's
// comments and a Pick button (TalkBar). All of it is read from the server as it is now, not from the
// sync feed, so a failure shows as one, not as an empty album.

import SwiftUI

/// The album's activity and picks, in a sheet over the album.
struct AlbumTalk: View {
    var album: AlbumRow
    var items: [Item] // the album's, to draw thumbnails as the library has them (blurred when adult)
    var open: (String) -> Void // an item, in the viewer
    @Environment(\.dismiss) private var dismiss
    @State private var tab = Tab.activity
    @State private var activity: [AlbumActivity]?
    @State private var picks: [AlbumPick]?
    @State private var problem = ""

    enum Tab { case activity, picks }

    private var owner: Bool { album.role == "owner" }
    /// The owner sees everyone's picks; anyone else only their own (the server also says which items
    /// others picked, without who).
    private var shownPicks: [AlbumPick]? { owner ? picks : picks?.filter(\.mine) }

    var body: some View {
        NavigationStack {
            List {
                if tab == .activity { activityRows } else { pickRows }
            }
            .listStyle(.plain)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    Picker("Show", selection: $tab) {
                        Text("Activity").tag(Tab.activity)
                        Text(shownPicks.map { $0.isEmpty ? "Picks" : "Picks · \($0.count)" } ?? "Picks").tag(Tab.picks)
                    }
                    .pickerStyle(.segmented)
                    .frame(maxWidth: 260)
                    .accessibilityIdentifier("talk-tabs")
                }
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
            .safeAreaInset(edge: .bottom) {
                if tab == .activity {
                    SayBox(prompt: "Say something about this album") { text in
                        guard let api = Library.shared.api() else { return }
                        try await api.comment(album.id, text)
                        await load()
                    }
                }
            }
            .task { await load() }
            .refreshable { await load() }
        }
    }

    /// A failure once there's something to show (a delete or a refresh that didn't go through).
    @ViewBuilder private var failed: some View {
        if !problem.isEmpty {
            Label("Couldn't reach Photostead: \(problem)", systemImage: "exclamationmark.triangle")
                .font(.footnote).foregroundStyle(.orange).listRowSeparator(.hidden)
                .accessibilityIdentifier("talk-problem")
        }
    }

    @ViewBuilder private var activityRows: some View {
        if let activity {
            failed
            if activity.isEmpty {
                Text("Nothing yet. Comments, and who adds what, show here.").foregroundStyle(.secondary).listRowSeparator(.hidden)
            }
            ForEach(activity) { a in
                VStack(alignment: .leading, spacing: 6) {
                    VStack(alignment: .leading, spacing: 6) {
                        (Text(a.who.isEmpty ? "Someone" : a.who).bold() + Text(" ") + Text(a.kind == "added" ? added(a.items ?? 0) : a.body ?? ""))
                            .fixedSize(horizontal: false, vertical: true)
                        Text(a.at.formatted(.relative(presentation: .named))).font(.caption).foregroundStyle(.secondary)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("talk-entry")
                    let shown = a.kind == "added" ? a.assets ?? [] : a.assetId.map { [$0] } ?? []
                    if !shown.isEmpty {
                        HStack(spacing: 4) {
                            ForEach(shown.prefix(5), id: \.self) { id in
                                Button { open(id) } label: { TalkThumb(id: id, items: items) }
                                    .buttonStyle(.plain)
                                    .accessibilityLabel("Open this item")
                                    .accessibilityIdentifier("talk-open")
                            }
                        }
                    }
                }
                .padding(.vertical, 4)
                .swipeActions {
                    if a.kind == "comment", a.mine == true, let id = a.commentID {
                        Button("Delete", role: .destructive) { Task { await delete(id) } }
                    }
                }
                .contextMenu {
                    if a.kind == "comment", a.mine == true, let id = a.commentID {
                        Button("Delete", systemImage: "trash", role: .destructive) { Task { await delete(id) } }
                    }
                }
            }
        } else {
            loading
        }
    }

    @ViewBuilder private var pickRows: some View {
        Text(owner ? "What the people you shared this with picked. Open an item to pick it yourself." : "Your picks, for whoever shared this. Open an item to pick it.")
            .font(.footnote).foregroundStyle(.secondary).listRowSeparator(.hidden)
        if let picks = shownPicks {
            failed
            if picks.isEmpty {
                Text(owner ? "No picks yet." : "You haven't picked any yet.").foregroundStyle(.secondary).listRowSeparator(.hidden)
            }
            ForEach(picks) { p in
                Button { open(p.assetId) } label: {
                    HStack(spacing: 12) {
                        TalkThumb(id: p.assetId, items: items, size: 64)
                        Text(owner ? p.people.joined(separator: ", ") : "Picked").foregroundStyle(.primary)
                        Spacer()
                        Image(systemName: "chevron.forward").font(.footnote.weight(.semibold)).foregroundStyle(.tertiary)
                    }
                }
                .accessibilityIdentifier("talk-pick")
            }
        } else {
            loading
        }
    }

    @ViewBuilder private var loading: some View {
        if problem.isEmpty {
            ProgressView().frame(maxWidth: .infinity).listRowSeparator(.hidden)
        } else {
            VStack(alignment: .leading, spacing: 8) {
                Text("Couldn't reach Photostead: \(problem)").foregroundStyle(.secondary)
                Button("Try again") { Task { await load() } }
            }
            .listRowSeparator(.hidden)
        }
    }

    private func added(_ n: Int) -> String { n == 1 ? "added an item" : "added \(n.formatted()) items" }

    private func load() async {
        guard let api = Library.shared.api() else { return }
        do {
            async let a = api.albumActivity(album.id)
            async let p = api.albumPicks(album.id)
            (activity, picks) = try await (a, p)
            problem = ""
        } catch {
            problem = error.localizedDescription
        }
    }

    private func delete(_ comment: String) async {
        guard let api = Library.shared.api() else { return }
        do {
            try await api.deleteComment(album.id, comment)
            await load()
        } catch {
            problem = error.localizedDescription
        }
    }
}

/// An item's comments in a shared album, with a box to say something about it.
struct ItemTalk: View {
    var album: AlbumRow
    var item: Item
    var counted: (Int) -> Void // how many now, for the viewer's button
    @Environment(\.dismiss) private var dismiss
    @State private var comments: [AlbumComment]?
    @State private var problem = ""

    var body: some View {
        NavigationStack {
            List {
                if let comments {
                    if !problem.isEmpty {
                        Label("Couldn't reach Photostead: \(problem)", systemImage: "exclamationmark.triangle")
                            .font(.footnote).foregroundStyle(.orange).listRowSeparator(.hidden)
                    }
                    if comments.isEmpty {
                        Text("No comments yet. Say something about this one.").foregroundStyle(.secondary).listRowSeparator(.hidden)
                    }
                    ForEach(comments) { c in
                        VStack(alignment: .leading, spacing: 4) {
                            (Text(c.author).bold() + Text(" ") + Text(c.body)).fixedSize(horizontal: false, vertical: true)
                            Text(c.createdAt.formatted(.relative(presentation: .named))).font(.caption).foregroundStyle(.secondary)
                        }
                        .padding(.vertical, 2)
                        .accessibilityElement(children: .combine)
                        .accessibilityIdentifier("item-comment")
                        .swipeActions {
                            if c.mine { Button("Delete", role: .destructive) { Task { await delete(c.id) } } }
                        }
                        .contextMenu {
                            if c.mine { Button("Delete", systemImage: "trash", role: .destructive) { Task { await delete(c.id) } } }
                        }
                    }
                } else if problem.isEmpty {
                    ProgressView().frame(maxWidth: .infinity).listRowSeparator(.hidden)
                } else {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Couldn't reach Photostead: \(problem)").foregroundStyle(.secondary)
                        Button("Try again") { Task { await load() } }
                    }
                    .listRowSeparator(.hidden)
                }
            }
            .listStyle(.plain)
            .navigationTitle("Comments")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
            .safeAreaInset(edge: .bottom) {
                SayBox(prompt: "Say something about this one") { text in
                    guard let api = Library.shared.api() else { return }
                    try await api.comment(album.id, text, item: item.id)
                    await load()
                }
            }
            .task { await load() }
        }
    }

    private func load() async {
        guard let api = Library.shared.api() else { return }
        do {
            let c = try await api.albumComments(album.id, item: item.id)
            comments = c
            problem = ""
            counted(c.count)
        } catch {
            problem = error.localizedDescription
        }
    }

    private func delete(_ comment: String) async {
        guard let api = Library.shared.api() else { return }
        do {
            try await api.deleteComment(album.id, comment)
            await load()
        } catch {
            problem = error.localizedDescription
        }
    }
}

/// Over the viewer, for an item in a shared album: Pick (yours, for the owner to see) and its comments.
struct TalkBar: View {
    var picked: Bool
    var comments: Int
    var pick: () -> Void
    var talk: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Button(action: pick) {
                Label(picked ? "Picked" : "Pick", systemImage: picked ? "checkmark.circle.fill" : "checkmark.circle")
            }
            .accessibilityIdentifier("pick")
            .accessibilityValue(picked ? "picked" : "")
            Button(action: talk) {
                Label(comments == 0 ? "Comment" : comments == 1 ? "1 comment" : "\(comments) comments", systemImage: "bubble.left")
            }
            .accessibilityIdentifier("comments")
        }
        .font(.subheadline.weight(.semibold))
        .foregroundStyle(.white)
        .labelStyle(.titleAndIcon)
        .buttonStyle(.plain)
        .padding(.horizontal, 16).padding(.vertical, 9)
        .background(.black.opacity(0.55), in: Capsule())
        .padding(.bottom, 10)
    }
}

/// A box to say something, and Send.
private struct SayBox: View {
    var prompt: String
    var say: (String) async throws -> Void
    @State private var text = ""
    @State private var sending = false
    @State private var problem = ""
    @FocusState private var focused: Bool

    private var trimmed: String { text.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if !problem.isEmpty { Text(problem).font(.caption).foregroundStyle(.red) }
            HStack(alignment: .bottom, spacing: 8) {
                TextField(prompt, text: $text, axis: .vertical)
                    .lineLimit(1...4)
                    .textFieldStyle(.roundedBorder)
                    .focused($focused)
                    .accessibilityIdentifier("talk-input")
                Button {
                    Task { await send() }
                } label: {
                    Image(systemName: "arrow.up.circle.fill").font(.title)
                }
                .disabled(trimmed.isEmpty || sending)
                .accessibilityLabel("Send")
                .accessibilityIdentifier("talk-send")
            }
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
        .background(.bar)
    }

    private func send() async {
        let said = String(trimmed.prefix(2000))
        guard !said.isEmpty else { return }
        sending = true
        defer { sending = false }
        do {
            try await say(said)
            text = ""
            problem = ""
            focused = false
        } catch {
            problem = "Not sent: \(error.localizedDescription)"
        }
    }
}

/// A small picture of one of the album's items (as the library draws it), by its id.
private struct TalkThumb: View {
    var id: String
    var items: [Item]
    var size: CGFloat = 52

    var body: some View {
        Color.clear
            .frame(width: size, height: size)
            .overlay {
                ItemPicture(item: items.first { $0.id == id } ?? Item(id: id, type: "image", takenAt: .distantPast, localDate: "", width: 0, height: 0,
                                                                    durationMs: 0, thumbhash: nil, motionID: nil, favorite: false, fileName: "", ai: ""))
            }
            .clipShape(RoundedRectangle(cornerRadius: 6))
    }
}
