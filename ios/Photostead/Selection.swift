// Choosing several, as the iPhone's Photos does: Select (or press and hold a tile), tap tiles to
// choose them, or drag across tiles to choose a run of them (a sideways drag; up and down still
// scrolls). Then a bar of actions along the bottom: share, favorite, add to an album, the trash, and
// More (the archive, the Household, stars and flags when they're on, the vault). In the trash the
// bar restores; in the vault it takes items out.

import SwiftUI

/// What's chosen, and whether choosing is on.
@MainActor
@Observable
final class Selection {
    var on = false
    private(set) var items: [String: Item] = [:]

    var count: Int { items.count }
    var ids: [String] { Array(items.keys) }
    func contains(_ id: String) -> Bool { items[id] != nil }

    func start(with item: Item? = nil) {
        on = true
        items = [:]
        if let item { items[item.id] = item }
    }

    func end() {
        on = false
        items = [:]
    }

    func toggle(_ item: Item) {
        if items[item.id] == nil { items[item.id] = item } else { items[item.id] = nil }
    }

    func set(_ chosen: [String: Item]) { items = chosen }
    var all: [Item] { Array(items.values) }
}

/// Where the tiles on screen are (in the grid's own space, "tiles"), by their place in the whole
/// list: what a drag across them is over. Not observed: tiles moving don't redraw anything.
@MainActor
final class TileFrames {
    var tiles: [Int: (rect: CGRect, item: Item)] = [:]

    func at(_ point: CGPoint) -> (index: Int, item: Item)? {
        tiles.first { $0.value.rect.contains(point) }.map { ($0.key, $0.value.item) }
    }
}

extension View {
    /// A tile that opens, or (while choosing) is chosen: tap, press and hold to start choosing,
    /// and its place noted for dragging across.
    func choosable(_ item: Item, at index: Int, _ selection: Selection, _ frames: TileFrames, open: @escaping (Item) -> Void) -> some View {
        modifier(Choosable(item: item, index: index, selection: selection, frames: frames, open: open))
    }

    /// Dragging sideways across tiles (while choosing) chooses the run between where it started and
    /// where it is; starting on a chosen one leaves them out instead. Scrolling stops meanwhile.
    func dragToChoose(_ selection: Selection, _ frames: TileFrames, lock: Binding<Bool>, order: @escaping (Int) -> Item?) -> some View {
        modifier(DragToChoose(selection: selection, frames: frames, lock: lock, order: order))
    }
}

private struct Choosable: ViewModifier {
    var item: Item
    var index: Int
    var selection: Selection
    var frames: TileFrames
    var open: (Item) -> Void

    func body(content: Content) -> some View {
        let chosen = selection.on && selection.contains(item.id)
        content
            .overlay {
                if chosen { Rectangle().fill(.white.opacity(0.25)).allowsHitTesting(false) }
            }
            .overlay(alignment: .topTrailing) { // only on chosen ones, as Photos does: clear of a video's length and the heart
                if chosen {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.title3)
                        .symbolRenderingMode(.palette)
                        .foregroundStyle(Color.onAccent, Color.accentColor) // not .white: the accent is near-white in dark mode
                        .shadow(radius: 2)
                        .padding(4)
                        .allowsHitTesting(false)
                }
            }
            .onTapGesture {
                if selection.on { selection.toggle(item) } else { open(item) }
            }
            .onLongPressGesture(minimumDuration: 0.4) {
                guard !selection.on else { return }
                UIImpactFeedbackGenerator(style: .medium).impactOccurred()
                selection.start(with: item)
            }
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .named("tiles")) } action: { frames.tiles[index] = ($0, item) }
            .onDisappear { frames.tiles[index] = nil }
            .accessibilityValue(chosen ? "chosen" : "")
    }
}

private struct DragToChoose: ViewModifier {
    var selection: Selection
    var frames: TileFrames
    @Binding var lock: Bool
    var order: (Int) -> Item?
    @State private var from: Int?
    @State private var choosing = true
    @State private var before: [String: Item] = [:]

    func body(content: Content) -> some View {
        content
            .coordinateSpace(.named("tiles"))
            .simultaneousGesture(drag, including: selection.on ? .all : .subviews)
    }

    private var drag: some Gesture {
        DragGesture(minimumDistance: 12, coordinateSpace: .named("tiles"))
            .onChanged { v in
                if from == nil {
                    guard abs(v.translation.width) > abs(v.translation.height) * 1.5, let start = frames.at(v.startLocation) else { return }
                    from = start.index
                    choosing = !selection.contains(start.item.id)
                    before = Dictionary(uniqueKeysWithValues: selection.all.map { ($0.id, $0) })
                    lock = true // stops the scroll this drag began
                }
                guard let from, let over = frames.at(v.location) else { return }
                var chosen = before
                for i in min(from, over.index)...max(from, over.index) {
                    guard let item = order(i) else { continue }
                    chosen[item.id] = choosing ? item : nil
                }
                if chosen.count != selection.count { UISelectionFeedbackGenerator().selectionChanged() }
                selection.set(chosen)
            }
            .onEnded { _ in
                from = nil
                lock = false
            }
    }
}

/// Where the chosen items are, which says what can be done with them.
enum Place {
    case library // the timeline, albums, lists, search
    case trash
    case vault
}

extension View {
    /// The bar for chosen items (and the tab bar out of the way meanwhile), and what its actions need.
    func choosing(_ selection: Selection, in place: Place = .library, done: (() async -> Void)? = nil) -> some View {
        modifier(ChoosingBar(selection: selection, place: place, done: done))
    }
}

private struct ChoosingBar: ViewModifier {
    var selection: Selection
    var place: Place
    var done: (() async -> Void)?
    @Environment(Library.self) private var library
    @State private var picking = false
    @State private var confirm: Confirm?
    @State private var sharing: [URL] = []
    @State private var working = ""
    @State private var note = ""
    @State private var needsPIN = false

    enum Confirm: Identifiable {
        case trash, vault
        var id: Self { self }
    }

    func body(content: Content) -> some View {
        content
            .toolbar {
                if selection.on {
                    ToolbarItem(placement: .principal) {
                        Text(selection.count == 0 ? "Choose items" : selection.count == 1 ? "1 chosen" : "\(selection.count.formatted()) chosen")
                            .font(.headline)
                            .accessibilityIdentifier("chosen-count")
                    }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Done") { selection.end() }.accessibilityIdentifier("choosing-done")
                    }
                    ToolbarItemGroup(placement: .bottomBar) { actions }
                }
            }
            .toolbar(selection.on ? .hidden : .automatic, for: .tabBar)
            .overlay {
                if !working.isEmpty || !note.isEmpty {
                    HStack(spacing: 8) {
                        if !working.isEmpty { ProgressView().tint(.white) }
                        Text(working.isEmpty ? note : working)
                    }
                    .font(.subheadline.weight(.medium)).foregroundStyle(.white)
                    .padding(.horizontal, 16).padding(.vertical, 10)
                    .background(.black.opacity(0.75), in: Capsule())
                    .accessibilityIdentifier("choosing-note")
                }
            }
            .sheet(isPresented: $needsPIN) {
                NewVaultPIN { run("Moved to the vault") { try await library.toVault($0) } }
            }
            .sheet(isPresented: $picking) {
                AlbumPicker { album in
                    picking = false
                    Task { await addToAlbum(album) }
                }
                .presentationDetents([.medium, .large])
            }
            .sheet(isPresented: Binding(get: { !sharing.isEmpty }, set: { if !$0 { sharing = [] } })) {
                ShareSheet(items: sharing).ignoresSafeArea()
            }
            .confirmationDialog(confirmTitle, isPresented: Binding(get: { confirm != nil }, set: { if !$0 { confirm = nil } }), titleVisibility: .visible) {
                switch confirm {
                case .trash: Button("Move to the trash", role: .destructive) { run("Moved to the trash") { try await library.trash($0) } }
                case .vault: Button("Move to the vault") { run("Moved to the vault") { try await library.toVault($0) } }
                case nil: EmptyView()
                }
            } message: {
                switch confirm {
                case .trash: Text("They're deleted for good after 30 days in the trash. Only your own items go there.")
                case .vault: Text("They leave the timeline, albums, search and sharing, and show only in the vault, behind your PIN.")
                case nil: EmptyView()
                }
            }
    }

    private var confirmTitle: String {
        let n = selection.count == 1 ? "it" : "these \(selection.count.formatted())"
        return confirm == .vault ? "Move \(n) to the vault?" : "Move \(n) to the trash?"
    }

    @ViewBuilder private var actions: some View {
        let none = selection.count == 0
        switch place {
        case .trash:
            Spacer()
            Button { run("Restored") { try await library.restore($0) } } label: { Label("Restore", systemImage: "arrow.uturn.backward") }
                .disabled(none).accessibilityIdentifier("chosen-restore")
            Spacer()
        case .vault:
            button("square.and.arrow.up", "Share") { Task { await share() } }.disabled(none)
            Spacer()
            Button { run("Taken out of the vault") { try await library.outOfVault($0) } } label: { Label("Take out of the vault", systemImage: "lock.open") }
                .disabled(none).accessibilityIdentifier("chosen-out-of-vault")
            Spacer()
        case .library:
            let favorites = !none && selection.all.allSatisfy(\.favorite)
            button("square.and.arrow.up", "Share") { Task { await share() } }.disabled(none)
            Spacer()
            button(favorites ? "heart.fill" : "heart", favorites ? "Unfavorite" : "Favorite") {
                run(favorites ? "Not favorites now" : "Favorites now") { try await library.setFavorite($0, !favorites) }
            }
            .disabled(none)
            Spacer()
            button("rectangle.stack.badge.plus", "Add to album") { picking = true }.disabled(none)
            Spacer()
            button("trash", "Move to the trash") { confirm = .trash }.disabled(none)
            Spacer()
            more.disabled(none)
        }
    }

    private func button(_ symbol: String, _ label: String, _ action: @escaping () -> Void) -> some View {
        Button(action: action) { Label(label, systemImage: symbol) }.accessibilityIdentifier("chosen-\(label)")
    }

    private var more: some View {
        let archived = selection.all.allSatisfy(\.archived)
        let shared = selection.all.allSatisfy(\.shared)
        return Menu {
            Button { run(archived ? "Back in the timeline" : "Archived") { try await library.archive($0, !archived) } } label: {
                Label(archived ? "Bring back from the archive" : "Archive", systemImage: archived ? "archivebox.fill" : "archivebox")
            }
            if library.inHousehold {
                Button { run(shared ? "Taken out of the Household" : "Shared with the Household") { try await library.shareWithHousehold($0, !shared) } } label: {
                    Label(shared ? "Take out of the Household" : "Share with the Household", systemImage: "house")
                }
            }
            if library.organize {
                Menu {
                    ForEach((1...5).reversed(), id: \.self) { n in
                        Button(String(repeating: "★", count: n)) { run("Rated") { try await library.mark($0, rating: n) } }
                    }
                    Button("No stars") { run("Stars taken off") { try await library.mark($0, rating: 0) } }
                } label: { Label("Rate", systemImage: "star") }
                Menu {
                    Button { run("Picked") { try await library.mark($0, flag: "pick") } } label: { Label("Pick", systemImage: "flag.fill") }
                    Button { run("Rejected") { try await library.mark($0, flag: "reject") } } label: { Label("Reject", systemImage: "xmark.circle") }
                    Button("No flag") { run("Flag taken off") { try await library.mark($0, flag: "none") } }
                } label: { Label("Flag", systemImage: "flag") }
                Menu {
                    ForEach(["red", "yellow", "green", "blue", "purple"], id: \.self) { color in
                        Button(color.capitalized) { run("Labeled \(color)") { try await library.mark($0, label: color) } }
                    }
                    Button("No label") { run("Label taken off") { try await library.mark($0, label: "none") } }
                } label: { Label("Color label", systemImage: "circle.fill") }
            }
            Button { Task { await toVault() } } label: { Label("Move to the vault", systemImage: "lock") }
        } label: {
            Label("More", systemImage: "ellipsis.circle")
        }
        .accessibilityIdentifier("chosen-more")
    }

    /// Does it to what's chosen (in pieces of 5,000, the server's limit), says what happened, and
    /// stops choosing.
    private func run(_ said: String, _ action: @escaping ([String]) async throws -> Void) {
        let ids = selection.ids
        guard !ids.isEmpty else { return }
        Task {
            working = "Working…"
            defer { working = "" }
            do {
                for start in stride(from: 0, to: ids.count, by: 5000) { try await action(Array(ids[start..<min(start + 5000, ids.count)])) }
                selection.end()
                show(said)
                await done?()
            } catch {
                show("Couldn’t: \(error.localizedDescription)")
            }
        }
    }

    /// The vault needs a PIN first: set here if it hasn't one.
    private func toVault() async {
        if let state = try? await library.api()?.vault(), !state.hasPin { needsPIN = true } else { confirm = .vault }
    }

    private func addToAlbum(_ album: AlbumPicker.Choice) async {
        let ids = selection.ids
        do {
            switch album {
            case .existing(let a):
                try await library.addToAlbum(a.id, ids)
                show("Added to \(a.title)")
            case .new(let title):
                try await library.createAlbum(title, with: ids)
                show("Added to \(title)")
            }
            selection.end()
        } catch {
            show("Couldn’t add them: \(error.localizedDescription)")
        }
    }

    /// The originals, through the iPhone's share sheet (got from Photostead first).
    private func share() async {
        guard let api = library.api() else { return }
        let items = selection.all
        var files: [URL] = []
        for (i, item) in items.enumerated() {
            working = items.count == 1 ? "Getting the original…" : "Getting \(i + 1) of \(items.count)…"
            do {
                var name = item.fileName
                if name.isEmpty { name = (try? await api.detail(item.id))?.fileName ?? "" }
                files.append(try await api.original(item.id, named: name))
            } catch {
                working = ""
                show("Couldn’t get them: \(error.localizedDescription)")
                return
            }
        }
        working = ""
        sharing = files
    }

    private func show(_ text: String) {
        note = text
        Task {
            try? await Task.sleep(for: .seconds(1.8))
            if note == text { withAnimation { note = "" } }
        }
    }
}
