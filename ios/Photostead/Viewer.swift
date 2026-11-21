// The viewer: one item at a time, full screen, handled like the iPhone's own Photos. Swipe sideways
// for the next; swipe down to put it back (the library shows through as it goes); swipe up for its
// details (Details.swift); pinch or double-tap to zoom and drag to look around; tap to hide the bars.
// A video plays; a Live Photo plays while it's pressed, or once from its LIVE badge (LivePhoto.swift).
// Its actions are the system's toolbar, so they sit where iOS puts toolbars: along the bottom, or
// down the side of an open iPhone Duo.

import AVKit
import SwiftUI

struct Viewer: View {
    var start: String
    var trash = false // in the trash: Restore instead of Trash
    var vault = false // in the vault: taken out of it, and no favorites or albums
    @State private var items: [Item]
    @Environment(\.dismiss) private var dismiss
    @State private var current: String?
    @State private var chrome = true
    @State private var zoomed = false
    @State private var drag: CGFloat = 0 // how far it's been pulled down (or up)
    @State private var info: Item?
    @State private var picking: Item? // an album for it
    @State private var confirmTrash = false
    @State private var sharing: URL?
    @State private var saving: URL? // on its way to Files
    @State private var working = "" // what's happening ("Getting the original…")
    @State private var note = "" // what just happened ("Added to Trip")
    @State private var live: String? // the Live Photo playing its clip
    var talk: AlbumRow? // opened from a shared album: Pick, and its comments
    @State private var picked: Set<String> = [] // yours, in that album
    @State private var said: [String: Int] = [:] // how many comments each item has there
    @State private var commenting: Item?

    init(items: [Item], start: String, trash: Bool = false, vault: Bool = false, talk: AlbumRow? = nil) {
        self.vault = vault
        self.talk = talk
        _items = State(initialValue: items)
        self.start = start
        self.trash = trash
    }

    private var item: Item? { items.first { $0.id == current } }
    private var pulled: CGFloat { max(0, drag) }

    var body: some View {
        NavigationStack {
            pages
                .toolbar { if let item { actions(item) } }
                .toolbar(.hidden, for: .navigationBar)
                .toolbar(chrome && drag == 0 && item != nil ? .visible : .hidden, for: .bottomBar)
                .modifier(SeeThrough())
        }
        .environment(\.colorScheme, .dark) // the viewer only: preferredColorScheme would darken the library behind it too
        .statusBarHidden(!chrome)
        .presentationBackground(.clear) // the library behind shows as it's pulled down
        .onAppear { current = start }
        .onChange(of: current) {
            zoomed = false
            live = nil
        }
        .task(id: current) { await heard() }
        .task { await learnPicks() }
        .sheet(item: $commenting) { item in
            if let talk {
                ItemTalk(album: talk, item: item) { n in said[item.id] = n }
                    .presentationDetents([.medium, .large])
            }
        }
        .sheet(item: $info) { item in Details(item: item) }
        .sheet(item: $picking) { item in
            AlbumPicker { album in
                Task { await add(item, to: album) }
            }
            .presentationDetents([.medium, .large])
        }
        .sheet(isPresented: Binding(get: { sharing != nil }, set: { if !$0 { sharing = nil } })) {
            if let sharing { ShareSheet(items: [sharing]).ignoresSafeArea() }
        }
        .sheet(isPresented: Binding(get: { saving != nil }, set: { if !$0 { saving = nil } })) {
            if let saving {
                SaveToFiles(file: saving) { saved in
                    try? FileManager.default.removeItem(at: saving.deletingLastPathComponent())
                    self.saving = nil
                    if saved { show("Saved to Files") }
                }
                .ignoresSafeArea()
            }
        }
        .confirmationDialog(trash ? "Put it back?" : "Move it to the trash?", isPresented: $confirmTrash, titleVisibility: .visible) {
            if trash {
                Button("Restore") { Task { await removeCurrent(restore: true) } }
            } else {
                Button("Move to the trash", role: .destructive) { Task { await removeCurrent(restore: false) } }
            }
        } message: {
            Text(trash ? "It goes back where it was." : "It's deleted for good after 30 days in the trash. Photostead keeps the original until then.")
        }
    }

    /// The items, side by side, with the date along the top and what's happening in the middle.
    private var pages: some View {
        ScrollView(.horizontal) {
            LazyHStack(spacing: 0) {
                ForEach(items) { item in
                    Page(item: item, showing: current == item.id, zoomed: $zoomed, chrome: $chrome, live: $live)
                        .containerRelativeFrame(.horizontal)
                }
            }
            .scrollTargetLayout()
        }
        .scrollTargetBehavior(.paging)
        .scrollPosition(id: $current)
        .scrollIndicators(.hidden)
        .scrollDisabled(zoomed || drag != 0)
        .offset(y: drag)
        .scaleEffect(1 - min(pulled, 400) / 1600)
        .simultaneousGesture(pull, including: zoomed ? .subviews : .all)
        .background(Color.black.opacity(1 - min(pulled / 350, 0.85)).ignoresSafeArea())
        .ignoresSafeArea()
        .overlay(alignment: .top) {
            if chrome && drag == 0, let item {
                Bar(item: item, live: live == item.id, play: { live = item.id }) { dismiss() }
                    .transition(.opacity)
            }
        }
        .overlay(alignment: .bottom) {
            if talk != nil, chrome && drag == 0, let item {
                TalkBar(picked: picked.contains(item.id), comments: said[item.id] ?? 0) {
                    Task { await pick(item) }
                } talk: {
                    commenting = item
                }
                .transition(.opacity)
            }
        }
        .overlay {
            if !working.isEmpty || !note.isEmpty {
                HStack(spacing: 8) {
                    if !working.isEmpty { ProgressView().tint(.white) }
                    Text(working.isEmpty ? note : working)
                }
                .font(.subheadline.weight(.medium)).foregroundStyle(.white)
                .padding(.horizontal, 16).padding(.vertical, 10)
                .background(.black.opacity(0.7), in: Capsule())
                .accessibilityIdentifier("viewer-note")
            }
        }
    }

    /// Share, favorite, details, add to an album, and the trash (or back from it).
    @ToolbarContentBuilder
    private func actions(_ item: Item) -> some ToolbarContent {
        ToolbarItemGroup(placement: .bottomBar) {
            action("square.and.arrow.up", "Share") { Task { await share(item) } }
            Spacer()
            Menu {
                Button { Task { await download(item, toPhotos: true) } } label: { Label("Save to Photos", systemImage: "photo.badge.arrow.down") }
                Button { Task { await download(item, toPhotos: false) } } label: { Label("Save to Files", systemImage: "folder") }
            } label: {
                Label("Download", systemImage: "arrow.down.circle")
            }
            .accessibilityIdentifier("Download")
            Spacer()
            if !trash && !vault {
                action(item.favorite ? "heart.fill" : "heart", item.favorite ? "Unfavorite" : "Favorite") { Task { await favorite(item) } }
                Spacer()
            }
            action("info.circle", "Details") { info = item }
            Spacer()
            if vault {
                action("lock.open", "Take out of the vault") { Task { await removeCurrent(restore: false, outOfVault: true) } }
                Spacer()
            } else if !trash {
                action("rectangle.stack.badge.plus", "Add to album") { picking = item }
                Spacer()
            }
            action(trash ? "arrow.uturn.backward" : "trash", trash ? "Restore" : "Move to the trash") { confirmTrash = true }
        }
    }

    private func action(_ symbol: String, _ label: String, _ run: @escaping () -> Void) -> some View {
        Button(action: run) { Label(label, systemImage: symbol) }
            .accessibilityIdentifier(label)
    }

    /// Down, past a little way (or flicked): put it back. Up: its details.
    private var pull: some Gesture {
        DragGesture(minimumDistance: 14)
            .onChanged { v in
                guard drag != 0 || abs(v.translation.height) > abs(v.translation.width) * 1.2 else { return }
                drag = v.translation.height > 0 ? v.translation.height : v.translation.height / 3
            }
            .onEnded { v in
                if drag > 110 || (drag > 20 && v.predictedEndTranslation.height > 380) {
                    dismiss()
                    return
                }
                if drag < -40 { info = item }
                withAnimation(.spring(duration: 0.3)) { drag = 0 }
            }
    }

    /// Your picks in the album the viewer was opened from.
    private func learnPicks() async {
        guard let talk, let api = Library.shared.api(), let picks = try? await api.albumPicks(talk.id) else { return }
        picked = Set(picks.filter(\.mine).map(\.assetId))
    }

    /// How many comments the item on screen has in that album.
    private func heard() async {
        guard let talk, let id = current, said[id] == nil, let api = Library.shared.api(),
              let comments = try? await api.albumComments(talk.id, item: id) else { return }
        said[id] = comments.count
    }

    /// Picks it for the album's owner to see, or takes the pick back.
    private func pick(_ item: Item) async {
        guard let talk, let api = Library.shared.api() else { return }
        let on = !picked.contains(item.id)
        if on { picked.insert(item.id) } else { picked.remove(item.id) }
        do {
            try await api.pick(talk.id, item.id, on)
            show(on ? "Picked" : "Not picked")
        } catch {
            if on { picked.remove(item.id) } else { picked.insert(item.id) }
            show("Couldn't reach Photostead")
        }
    }

    private func show(_ text: String) {
        note = text
        Task {
            try? await Task.sleep(for: .seconds(1.6))
            if note == text { withAnimation { note = "" } }
        }
    }

    /// The original, through the iPhone's share sheet (to Messages, Files, another app…).
    private func share(_ item: Item) async {
        guard let api = Library.shared.api() else { return }
        working = item.isVideo ? "Getting the video…" : "Getting the original…"
        defer { working = "" }
        do {
            var name = item.fileName
            if name.isEmpty { name = (try? await api.detail(item.id))?.fileName ?? "" }
            sharing = try await api.original(item.id, named: name)
        } catch {
            show("Couldn’t get it: \(error.localizedDescription)")
        }
    }

    /// At full size: the edited photo when it's been edited, otherwise the original.
    private func download(_ item: Item, toPhotos: Bool) async {
        guard let api = Library.shared.api() else { return }
        working = item.isVideo ? "Getting the video…" : "Getting it at full size…"
        do {
            let file = try await Download.file(item, api: api)
            if toPhotos {
                defer { try? FileManager.default.removeItem(at: file.deletingLastPathComponent()) }
                working = "Saving to Photos…"
                try await Download.toPhotos(file, video: item.isVideo)
                working = ""
                show("Saved to Photos")
            } else {
                working = ""
                saving = file
            }
        } catch {
            working = ""
            show("Couldn’t save it: \(error.localizedDescription)")
        }
    }

    private func favorite(_ item: Item) async {
        let on = !item.favorite
        if let i = items.firstIndex(where: { $0.id == item.id }) { items[i].favorite = on }
        do {
            try await Library.shared.setFavorite([item.id], on)
        } catch {
            if let i = items.firstIndex(where: { $0.id == item.id }) { items[i].favorite = !on }
            show("Couldn’t change it: \(error.localizedDescription)")
        }
    }

    private func add(_ item: Item, to album: AlbumPicker.Choice) async {
        picking = nil
        do {
            switch album {
            case .existing(let a):
                try await Library.shared.addToAlbum(a.id, [item.id])
                show("Added to \(a.title)")
            case .new(let title):
                try await Library.shared.createAlbum(title, with: [item.id])
                show("Added to \(title)")
            }
        } catch {
            show("Couldn’t add it: \(error.localizedDescription)")
        }
    }

    /// To the trash (or, in the trash, back; or out of the vault): it leaves the viewer, which moves
    /// on to the next.
    private func removeCurrent(restore: Bool, outOfVault: Bool = false) async {
        guard let item, let i = items.firstIndex(where: { $0.id == item.id }) else { return }
        do {
            if outOfVault {
                try await Library.shared.outOfVault([item.id])
            } else if restore {
                try await Library.shared.restore([item.id])
            } else {
                try await Library.shared.trash([item.id])
            }
        } catch {
            show("Couldn’t: \(error.localizedDescription)")
            return
        }
        items.remove(at: i)
        if items.isEmpty {
            dismiss()
            return
        }
        current = items[min(i, items.count - 1)].id
        show(outOfVault ? "Taken out of the vault" : restore ? "Restored" : "Moved to the trash")
    }
}

/// The navigation stack shows nothing of its own, so the library shows through as an item is
/// pulled down (iOS 18 and later; before, it's dark behind).
private struct SeeThrough: ViewModifier {
    func body(content: Content) -> some View {
        if #available(iOS 18.0, *) {
            content.containerBackground(.clear, for: .navigation)
        } else {
            content
        }
    }
}

/// An album to add to: yours (and ones you may add to), or a new one.
struct AlbumPicker: View {
    enum Choice {
        case existing(AlbumRow)
        case new(String)
    }

    var pick: (Choice) -> Void
    @Environment(Library.self) private var library
    @Environment(\.dismiss) private var dismiss
    @State private var title = ""

    var body: some View {
        NavigationStack {
            List {
                Section {
                    HStack {
                        TextField("New album", text: $title).submitLabel(.done).onSubmit(make)
                        Button("Make", action: make).disabled(title.trimmingCharacters(in: .whitespaces).isEmpty)
                    }
                }
                Section {
                    ForEach(library.albums.filter { $0.kind == "manual" && $0.role != "viewer" }) { album in
                        Button { pick(.existing(album)) } label: {
                            HStack(spacing: 12) {
                                Group {
                                    if let cover = album.cover { ItemPicture(item: cover) } else { Rectangle().fill(.quaternary) }
                                }
                                .frame(width: 44, height: 44).clipShape(RoundedRectangle(cornerRadius: 8))
                                VStack(alignment: .leading) {
                                    Text(album.title).foregroundStyle(.primary)
                                    Text("\(album.count.formatted()) items").font(.caption).foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                }
            }
            .navigationTitle("Add to album")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } } }
        }
    }

    private func make() {
        let t = title.trimmingCharacters(in: .whitespaces)
        if !t.isEmpty { pick(.new(t)) }
    }
}

/// The iPhone's share sheet.
struct ShareSheet: UIViewControllerRepresentable {
    var items: [Any]
    func makeUIViewController(context: Context) -> UIActivityViewController { UIActivityViewController(activityItems: items, applicationActivities: nil) }
    func updateUIViewController(_ controller: UIActivityViewController, context: Context) {}
}

/// The date, time and file name, the badges, and the way out.
private struct Bar: View {
    var item: Item
    var live = false // its clip is playing
    var play: () -> Void = {}
    var close: () -> Void
    @Environment(\.dynamicTypeSize) private var size

    private var badged: Bool { item.motionID != nil || !item.ai.isEmpty || item.favorite }

    var body: some View {
        HStack(alignment: size.isAccessibilitySize ? .top : .center, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text(item.takenAt.formatted(date: .long, time: .omitted)).font(.headline)
                Text(item.takenAt.formatted(date: .omitted, time: .shortened) + (item.fileName.isEmpty ? "" : " · \(item.fileName)"))
                    .font(.caption).foregroundStyle(.white.opacity(0.7)).lineLimit(1)
                if size.isAccessibilitySize && badged { badges.padding(.top, 4) } // under the date, so the date keeps the width
            }
            Spacer()
            if !size.isAccessibilitySize { badges }
            Button(action: close) {
                Image(systemName: "xmark").font(.body.weight(.semibold)).frame(width: 36, height: 36).background(.white.opacity(0.15), in: Circle())
            }
            .accessibilityLabel("Close")
            .accessibilityIdentifier("close")
        }
        .foregroundStyle(.white)
        .padding(.horizontal, 16).padding(.vertical, 8)
        .background(LinearGradient(colors: [.black.opacity(0.6), .clear], startPoint: .top, endPoint: .bottom).ignoresSafeArea())
    }

    /// LIVE, AI and the heart.
    private var badges: some View {
        HStack(spacing: 10) {
            if item.motionID != nil && !Library.shared.veiled(item) { LiveBadge(playing: live, play: play) }
            if !item.ai.isEmpty { Badge(text: "AI") }
            if item.favorite { Image(systemName: "heart.fill").accessibilityLabel("Favorite") }
        }
    }
}

private struct Badge: View {
    var text: String
    var body: some View {
        Text(text).font(.caption2.weight(.bold)).padding(.horizontal, 6).padding(.vertical, 2).background(.white.opacity(0.2), in: Capsule())
    }
}

private struct Page: View {
    var item: Item
    var showing: Bool
    @Binding var zoomed: Bool
    @Binding var chrome: Bool
    @Binding var live: String?

    var body: some View {
        Group {
            if veiled { // adult, for someone who blurs it: nothing plays until Show
                ItemPicture(item: item, what: "preview", contentMode: .fit)
                    .contentShape(Rectangle())
                    .onTapGesture { withAnimation(.easeOut(duration: 0.15)) { chrome.toggle() } }
            } else if item.isVideo && showing, let url = Library.shared.url(item.id, "original"), let token = Library.shared.token {
                Player(url: url, token: token)
            } else if item.isVideo {
                ItemPicture(item: item, what: "preview", contentMode: .fit)
                    .overlay { Image(systemName: "play.circle.fill").font(.system(size: 56)).foregroundStyle(.white.opacity(0.85)) }
            } else {
                Zoomable(item: item, showing: showing, zoomed: $zoomed, chrome: $chrome, live: $live)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityIdentifier(showing ? "shown" : "page")
        .overlay { // over the page's own identifier, so Show keeps its own
            if veiled { Veil { withAnimation(.easeOut(duration: 0.25)) { _ = Library.shared.revealed.insert(item.id) } } }
        }
    }

    private var veiled: Bool { Library.shared.veiled(item) }
}

/// Over an adult item's blurred picture: what it is, and Show (until the app is closed, as on the web
/// until the page is reloaded).
private struct Veil: View {
    var show: () -> Void

    var body: some View {
        Button(action: show) {
            VStack(spacing: 8) {
                Image(systemName: "eye.slash").font(.title2)
                Text("Adult content").font(.headline)
                Text("Show").font(.subheadline.weight(.semibold))
                    .padding(.horizontal, 16).padding(.vertical, 7)
                    .background(.white.opacity(0.22), in: Capsule())
            }
            .foregroundStyle(.white)
            .padding(24)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .shadow(color: .black.opacity(0.45), radius: 6)
        .accessibilityLabel("Show adult content")
        .accessibilityIdentifier("show-adult")
    }
}

/// A photo that pinches and double-taps to zoom, drags around when zoomed, and (a Live Photo)
/// plays its clip while it's pressed (LivePhoto.swift).
private struct Zoomable: View {
    var item: Item
    var showing: Bool
    @Binding var zoomed: Bool
    @Binding var chrome: Bool
    @Binding var live: String?
    @State private var scale: CGFloat = 1
    @State private var settled: CGFloat = 1
    @State private var offset: CGSize = .zero
    @State private var dragged: CGSize = .zero
    @State private var holding: Task<Void, Never>?

    private var playing: Bool { live == item.id }

    var body: some View {
        GeometryReader { box in
            ItemPicture(item: item, what: "preview", contentMode: .fit)
                .overlay {
                    if showing, let id = item.motionID, let url = Library.shared.url(id, "original"), let token = Library.shared.token {
                        LiveMotion(url: url, token: token, playing: playing) { if live == item.id { live = nil } }
                            .id(id)
                            .allowsHitTesting(false)
                    }
                }
                .frame(width: box.size.width, height: box.size.height)
                .scaleEffect(scale)
                .offset(offset)
                .gesture(magnify)
                .gesture(pan(in: box.size), including: scale > 1 ? .all : .subviews)
                .onTapGesture(count: 2) { location in
                    withAnimation(.spring(duration: 0.3)) {
                        if scale > 1 {
                            reset()
                        } else { // in on where it was tapped
                            scale = 2.5
                            settled = 2.5
                            offset = clamp(CGSize(width: (box.size.width / 2 - location.x) * 1.5, height: (box.size.height / 2 - location.y) * 1.5), in: box.size)
                            dragged = offset
                            zoomed = true
                        }
                    }
                }
                .onTapGesture { withAnimation(.easeOut(duration: 0.15)) { chrome.toggle() } }
                // Never finishes, so it only says when a finger is down: a Live Photo plays while it
                // stays down (after a moment, so taps and swipes don't start it) and stops when it lifts.
                .onLongPressGesture(minimumDuration: .infinity, maximumDistance: 12, perform: {}, onPressingChanged: press)
                .sensoryFeedback(.impact(weight: .light), trigger: playing) { _, now in now }
                .accessibilityActions {
                    if item.motionID != nil { Button("Play the Live Photo") { live = item.id } }
                }
        }
        .clipped()
        .onChange(of: showing) {
            if !showing {
                reset()
                if playing { live = nil }
            }
        }
    }

    private func press(_ down: Bool) {
        holding?.cancel()
        guard item.motionID != nil, scale == 1 else { return }
        if down {
            holding = Task {
                try? await Task.sleep(for: .milliseconds(200))
                if !Task.isCancelled { live = item.id }
            }
        } else if playing {
            live = nil
        }
    }

    private var magnify: some Gesture {
        MagnifyGesture()
            .onChanged { v in scale = min(6, max(1, settled * v.magnification)) }
            .onEnded { _ in
                withAnimation(.spring(duration: 0.25)) {
                    if scale < 1.05 { reset() } else { settled = scale; zoomed = true }
                }
            }
    }

    private func pan(in size: CGSize) -> some Gesture {
        DragGesture()
            .onChanged { v in offset = CGSize(width: dragged.width + v.translation.width, height: dragged.height + v.translation.height) }
            .onEnded { _ in
                withAnimation(.spring(duration: 0.25)) { offset = clamp(offset, in: size) }
                dragged = offset
            }
    }

    /// No further than the photo's edge.
    private func clamp(_ o: CGSize, in size: CGSize) -> CGSize {
        let mx = size.width * (scale - 1) / 2, my = size.height * (scale - 1) / 2
        return CGSize(width: min(mx, max(-mx, o.width)), height: min(my, max(-my, o.height)))
    }

    private func reset() {
        scale = 1
        settled = 1
        offset = .zero
        dragged = .zero
        zoomed = false
    }
}

/// A video from Photostead, streamed (the server answers byte ranges), with the device's token.
struct Player: View {
    @State private var player: AVPlayer

    init(url: URL, token: String) {
        let asset = AVURLAsset(url: url, options: ["AVURLAssetHTTPHeaderFieldsKey": ["Authorization": "Bearer \(token)"]])
        _player = State(initialValue: AVPlayer(playerItem: AVPlayerItem(asset: asset)))
    }

    var body: some View {
        VideoPlayer(player: player)
            .onAppear { player.play() }
            .onDisappear { player.pause() }
    }
}
