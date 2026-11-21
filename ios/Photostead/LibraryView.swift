// Your Photostead library on the phone: the timeline by month, with Memories across the top and a
// filter (all, photos, videos, favorites; everyone's, yours or the Household's), and albums. Items
// open in the viewer (Viewer.swift); Select (or press and hold) chooses several (Selection.swift).

import SwiftUI

struct LibraryView: View {
    var toTop = 0 // goes up when the Library tab is tapped again
    @Environment(Library.self) private var library
    @State private var open: Viewing?
    @AppStorage("columns") private var columns = 4
    @State private var seen = Seen() // the month in view, without redrawing the grid as it changes
    @State private var scroll = Scrolling() // read by the scrubber only: the grid isn't redrawn as it scrolls
    @State private var selection = Selection()
    @State private var frames = TileFrames()
    @State private var lock = false // a drag across tiles is choosing them: no scrolling meanwhile

    var body: some View {
        NavigationStack {
            ScrollViewReader { proxy in
                ScrollView {
                    Color.clear.frame(height: 0).id("top")
                    if !library.memories.isEmpty && !selection.on {
                        MemoriesStrip(memories: library.memories) { m in
                            let items = m.assets.map(\.item)
                            if let first = items.first { open = Viewing(items: items, start: first.id) }
                        }
                        .padding(.bottom, 8)
                    }
                    HStack(spacing: 8) {
                        BackupPill()
                        FilterPill()
                        Spacer()
                    }
                    .padding(.horizontal, 16).padding(.bottom, 4)
                    if library.total == 0 {
                        Empty(syncing: library.syncing, received: library.received, message: library.message)
                    } else if library.timeline.isEmpty {
                        Text("Nothing here with this filter.").foregroundStyle(.secondary).padding(60)
                    } else {
                        Grid(months: library.months, columns: columns, seen: seen, selection: selection, frames: frames, lock: $lock) { item in
                            open = Viewing(items: library.timeline, start: item.id)
                        }
                    }
                }
                .scrollIndicators(.hidden)
                .scrollDisabled(lock)
                .modifier(Watch(scroll: scroll))
                .overlay(alignment: .trailing) {
                    if library.months.count > 1 {
                        Scrubber(months: library.months, total: library.timeline.count, scroll: scroll) { proxy.scrollTo($0, anchor: .top) }
                    }
                }
                .simultaneousGesture(MagnifyGesture().onEnded { v in // pinch: more or fewer across
                    let steps = [2, 3, 4, 5, 7]
                    let at = steps.firstIndex(of: columns) ?? 2
                    let month = seen.current
                    if v.magnification > 1.15, at > 0 { withAnimation(.snappy) { columns = steps[at - 1] } }
                    if v.magnification < 0.87, at < steps.count - 1 { withAnimation(.snappy) { columns = steps[at + 1] } }
                    if let month { DispatchQueue.main.async { proxy.scrollTo(month, anchor: .top) } } // stay on the same month
                })
                .onChange(of: toTop) { withAnimation { proxy.scrollTo("top", anchor: .top) } }
                .onGeometryChange(for: CGFloat.self) { $0.frame(in: .global).minY + $0.safeAreaInsets.top } action: { seen.edge = $0 }
                .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { _ in // turned or folded: back to the month that was at the top
                    DispatchQueue.main.async { if let month = seen.before { proxy.scrollTo(month, anchor: .top) } } // once the headers have said where they are now
                }
            }
            .navigationTitle("Library")
            .toolbar {
                if !selection.on {
                    ToolbarItem(placement: .topBarTrailing) {
                        if library.syncing { ProgressView().accessibilityLabel("Catching up") }
                    }
                    ToolbarItem(placement: .topBarTrailing) { FilterMenu() }
                    ToolbarItem(placement: .topBarTrailing) {
                        Button("Select") { selection.start() }.disabled(library.timeline.isEmpty).accessibilityIdentifier("select")
                    }
                    ToolbarItem(placement: .topBarTrailing) { AccountButton() }
                }
            }
            .choosing(selection)
            .refreshable { await library.sync() }
            .task { if library.total == 0 { await library.sync() } } // nothing here yet: get it
            .task { await library.refreshExtras() } // memories, who you are, the Household
        }
        .fullScreenCover(item: $open) { v in Viewer(items: v.items, start: v.start) }
    }
}

/// The right edge: a mark for each year, and dragging along it goes straight to a month (with the
/// month shown beside the finger), as the web app's timeline does.
struct Scrubber: View {
    var months: [Month]
    var total: Int
    var scroll = Scrolling()
    var jump: (String) -> Void
    @State private var at: (month: Month, y: CGFloat)?

    /// Where each month starts, as a share of all the items (newest at the top).
    private var starts: [(Month, CGFloat)] {
        var seen = 0
        return months.map { m in
            defer { seen += m.items.count }
            return (m, CGFloat(seen) / CGFloat(max(total, 1)))
        }
    }

    var body: some View {
        GeometryReader { box in
            let h = box.size.height - 24
            ZStack(alignment: .topTrailing) {
                ForEach(Array(years(height: h).enumerated()), id: \.offset) { _, mark in
                    HStack(spacing: 5) {
                        if at != nil, let year = mark.0 { // the years, while dragging
                            Text(year).font(.system(size: 10, weight: .semibold).monospacedDigit()).foregroundStyle(.secondary)
                                .lineLimit(1).fixedSize()
                                .padding(.horizontal, 4).background(.regularMaterial, in: Capsule())
                        }
                        Rectangle().fill(.secondary.opacity(at == nil ? 0.5 : 0.9)).frame(width: mark.0 == nil ? 4 : 8, height: 1)
                    }
                    .fixedSize()
                    .offset(y: 12 + mark.1)
                }
                if at == nil && scroll.moving { // where the grid is: grab it to fly through the years
                    Capsule().fill(.regularMaterial).overlay(Capsule().strokeBorder(.secondary.opacity(0.4)))
                        .frame(width: 8, height: 44)
                        .shadow(radius: 2)
                        .offset(x: -2, y: 12 + scroll.fraction * (h - 44))
                        .transition(.opacity)
                }
                if let at {
                    Text(at.month.title).font(.footnote.weight(.semibold)).padding(.horizontal, 12).padding(.vertical, 7)
                        .background(.regularMaterial, in: Capsule())
                        .fixedSize()
                        .offset(x: -48, y: max(0, 12 + at.y - 16))
                        .accessibilityIdentifier("scrubbing")
                }
            }
            .frame(width: box.size.width, height: box.size.height, alignment: .topTrailing)
            .padding(.trailing, 4)
            .contentShape(Rectangle())
            .gesture(DragGesture(minimumDistance: 0).onChanged { v in
                let f = min(1, max(0, (v.location.y - 12) / h))
                guard let m = starts.last(where: { $0.1 <= f })?.0 ?? months.first else { return }
                if at?.month.id != m.id {
                    UISelectionFeedbackGenerator().selectionChanged()
                    jump(m.id)
                }
                at = (m, f * h)
            }.onEnded { _ in withAnimation(.easeOut(duration: 0.3)) { at = nil } })
        }
        .frame(width: 30)
        .padding(.vertical, 8)
        .accessibilityElement()
        .accessibilityLabel("Dates")
        .accessibilityIdentifier("scrubber")
    }

    /// A mark at each year's start, labeled when there's room.
    private func years(height: CGFloat) -> [(String?, CGFloat)] {
        var out: [(String?, CGFloat)] = []
        var lastLabel: CGFloat = -100
        var lastYear = ""
        for (m, f) in starts {
            let year = String(m.id.prefix(4))
            guard year != lastYear else { continue }
            lastYear = year
            let y = f * height
            if y - lastLabel >= 14 {
                out.append((year, y))
                lastLabel = y
            } else {
                out.append((nil, y))
            }
        }
        return out
    }
}

/// Which month is at the top: the one whose header is pinned there (or was just pushed up by the
/// next). Kept from the headers' positions without redrawing the grid as they move.
final class Seen {
    var edge: CGFloat = 0 // the top of what shows, below the navigation bar
    private var width: CGFloat = 0
    private var tops: [String: CGFloat] = [:]
    /// At the top now (none at the very top of the library, above the first header).
    private(set) var current: String?
    /// At the top before the grid last changed width (turned, folded), to go back to.
    private(set) var before: String?

    func header(_ month: String, at y: CGFloat, width w: CGFloat) {
        if w != width { // a new layout: the positions so far are the old one's
            if width > 0 { before = current }
            width = w
            tops = [:]
        }
        tops[month] = y
        current = tops.filter { $0.value <= edge + 2 }.max { $0.value < $1.value }?.key
    }

    func gone(_ month: String) { tops[month] = nil }
}

/// How far down the grid is, and whether it's moving (the scrubber's handle shows then).
@Observable
final class Scrolling {
    var fraction: CGFloat = 0
    var moving = false
    @ObservationIgnored private var still: Task<Void, Never>?

    @MainActor func moved(to f: CGFloat) {
        fraction = min(1, max(0, f))
        if !moving { moving = true }
        still?.cancel()
        still = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(1.5))
            if !Task.isCancelled { withAnimation(.easeOut(duration: 0.3)) { self?.moving = false } }
        }
    }
}

/// Follows the grid's scrolling (iOS 18 and later).
private struct Watch: ViewModifier {
    var scroll: Scrolling

    func body(content: Content) -> some View {
        if #available(iOS 18.0, *) {
            content.onScrollGeometryChange(for: CGFloat.self) { g in
                g.contentOffset.y / max(1, g.contentSize.height - g.containerSize.height)
            } action: { _, f in
                scroll.moved(to: f)
            }
        } else {
            content
        }
    }
}

struct Viewing: Identifiable {
    var items: [Item]
    var start: String
    var id: String { start }
}

private struct Empty: View {
    var syncing: Bool
    var received: Int
    var message: String

    var body: some View {
        VStack(spacing: 12) {
            if syncing {
                ProgressView()
                Text(received > 0 ? "Getting your library · \(received.formatted()) items" : "Getting your library…").foregroundStyle(.secondary)
            } else {
                Image(systemName: "photo.on.rectangle").font(.largeTitle).foregroundStyle(.secondary)
                Text(message.isEmpty ? "Nothing here yet. Pull down to check again." : message).foregroundStyle(.secondary).multilineTextAlignment(.center)
            }
        }
        .padding(40)
        .frame(maxWidth: .infinity)
        .padding(.top, 120)
    }
}

/// Tiles across: as many as the phone held upright shows at this size (four, or as pinched), and
/// more in a wider window (the phone turned, or an iPhone Duo opened), so the tiles stay the size
/// they were.
func across(_ upright: Int, in width: CGFloat) -> Int {
    guard width > 0 else { return upright }
    return max(upright, Int((width * CGFloat(upright) / 402).rounded()))
}

/// Square tiles in a grid, as many across as its width takes (see `across`).
struct Tiles<Content: View>: View {
    var upright = 4
    var pinned: PinnedScrollableViews = []
    @ViewBuilder var content: () -> Content
    @State private var width: CGFloat = 0

    var body: some View {
        LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 2), count: across(upright, in: width)), spacing: 2, pinnedViews: pinned, content: content)
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
    }
}

/// Items in square tiles (four across with the phone upright, or as pinched), under a header for
/// each month.
struct Grid: View {
    var months: [Month]
    var columns = 4
    var seen = Seen()
    var selection = Selection()
    var frames = TileFrames()
    var lock: Binding<Bool> = .constant(false)
    var open: (Item) -> Void

    var body: some View {
        Tiles(upright: columns, pinned: [.sectionHeaders]) {
            ForEach(months) { month in
                Section {
                    ForEach(Array(month.items.enumerated()), id: \.element.id) { i, item in
                        Tile(item: item).choosable(item, at: month.start + i, selection, frames, open: open)
                    }
                } header: {
                    HStack(alignment: .firstTextBaseline) {
                        Text(month.title).font(.title3.weight(.semibold))
                        Text(month.items.count.formatted()).font(.footnote.monospacedDigit()).foregroundStyle(.secondary)
                        Spacer()
                    }
                    .padding(.horizontal, 16).padding(.vertical, 10)
                    .background(.bar, ignoresSafeAreaEdges: .vertical) // not under a status bar at the side (an open iPhone Duo)
                    .id(month.id)
                    .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { seen.header(month.id, at: $0.minY, width: $0.width) }
                    .onDisappear { seen.gone(month.id) }
                }
            }
        }
        .dragToChoose(selection, frames, lock: lock) { i in
            guard let m = months.last(where: { $0.start <= i }), i - m.start < m.items.count else { return nil }
            return m.items[i - m.start]
        }
    }
}

struct Tile: View {
    var item: Item
    @Environment(Library.self) private var library

    private var veiled: Bool { library.veiled(item) }

    var body: some View {
        Color.clear
            .aspectRatio(1, contentMode: .fit)
            .overlay { ItemPicture(item: item) }
            .clipped()
            .overlay(alignment: .bottom) {
                if item.favorite || item.isVideo {
                    // One row, so the heart and a video's length can't meet; when the tile's too narrow
                    // for both (pinched small, large text), the length alone.
                    ViewThatFits(in: .horizontal) {
                        corner(heart: item.favorite)
                        corner(heart: !item.isVideo)
                    }
                    .padding(5)
                }
            }
            .overlay(alignment: .topLeading) {
                if library.organize && (item.rating > 0 || !item.flag.isEmpty || !item.label.isEmpty) { Marks(item: item).padding(4) }
            }
            .overlay(alignment: .topTrailing) {
                if item.motionID != nil { Image(systemName: "livephoto").font(.caption2.weight(.semibold)).foregroundStyle(.white).shadow(radius: 2).padding(5) }
            }
            .overlay {
                if veiled { Image(systemName: "eye.slash").font(.body.weight(.semibold)).foregroundStyle(.white.opacity(0.9)).shadow(radius: 3) }
            }
            .dynamicTypeSize(...DynamicTypeSize.xxLarge) // marks on a picture: they grow a little with the text, never past the tile
            .contentShape(Rectangle())
            .accessibilityElement()
            .accessibilityLabel("\(veiled ? "Adult content, blurred, " : "")\(item.isVideo ? "Video" : item.motionID != nil ? "Live Photo" : "Photo"), \(item.takenAt.formatted(date: .abbreviated, time: .shortened))")
            .accessibilityIdentifier("tile")
            .accessibilityAddTraits(.isButton)
    }

    /// Along the bottom: the heart on the left, a video's length on the right.
    private func corner(heart: Bool) -> some View {
        HStack(spacing: 4) {
            if heart { Image(systemName: "heart.fill") }
            Spacer(minLength: 0)
            if item.isVideo { Text(duration(item.durationMs)).monospacedDigit().lineLimit(1) }
        }
        .font(.caption2.weight(.semibold))
        .foregroundStyle(.white)
        .shadow(radius: 2)
    }
}

func duration(_ ms: Int) -> String {
    let s = max(0, ms / 1000)
    return s >= 3600 ? String(format: "%d:%02d:%02d", s / 3600, s / 60 % 60, s % 60) : String(format: "%d:%02d", s / 60, s % 60)
}

// MARK: Albums

struct AlbumCard: View {
    var album: AlbumRow

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Color.clear.aspectRatio(1, contentMode: .fit)
                .overlay {
                    if let cover = album.cover {
                        ItemPicture(item: cover)
                    } else {
                        Rectangle().fill(.quaternary).overlay { Image(systemName: "rectangle.stack").foregroundStyle(.secondary) }
                    }
                }
                .clipShape(RoundedRectangle(cornerRadius: 12))
            Text(album.title).font(.subheadline.weight(.semibold)).lineLimit(1)
            Text(album.kind == "smart" ? "Smart album" : "\(album.count.formatted()) items\(album.role == "owner" ? "" : " · shared with you")")
                .font(.caption).foregroundStyle(.secondary)
        }
        .accessibilityIdentifier("album")
    }
}

struct AlbumView: View {
    var album: AlbumRow
    @State private var items: [Item] = []
    @State private var open: Viewing?
    @State private var selection = Selection()
    @State private var frames = TileFrames()
    @State private var lock = false
    @State private var sharing = false
    @State private var adding = false
    @State private var talks = false // shared, with you or by you: its activity and picks
    @State private var talking = false
    @State private var opening: String? // an item from the activity, once its sheet has gone

    private var canAdd: Bool { album.kind == "manual" && (album.role == "owner" || album.role == "contributor") }

    var body: some View {
        ScrollView {
            TileList(items: items, selection: selection, frames: frames, lock: $lock) { item in open = Viewing(items: items, start: item.id) }
        }
        .scrollDisabled(lock)
        .navigationTitle(album.title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if !selection.on {
                if canAdd {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button { adding = true } label: { Label("Add from this iPhone", systemImage: "plus") }.accessibilityIdentifier("add-to-album")
                    }
                }
                if album.role == "owner" && album.kind == "manual" {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button { sharing = true } label: { Label("Share", systemImage: "person.crop.circle.badge.plus") }.accessibilityIdentifier("share-album")
                    }
                }
                if talks {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button { talking = true } label: { Label("Activity", systemImage: "bubble.left.and.bubble.right") }.accessibilityIdentifier("album-activity")
                    }
                }
                if !items.isEmpty { ToolbarItem(placement: .topBarTrailing) { Button("Select") { selection.start() } } }
            }
        }
        .choosing(selection) { await load() }
        .sheet(isPresented: $sharing, onDismiss: { Task { await learn() } }) { AlbumSharing(album: album) }
        .sheet(isPresented: $talking, onDismiss: {
            if let id = opening, items.contains(where: { $0.id == id }) { open = Viewing(items: items, start: id) }
            opening = nil
        }) {
            AlbumTalk(album: album, items: items) { id in
                opening = id
                talking = false
            }
        }
        .modifier(AddFromPhone(album: album, picking: $adding) { Task { await Library.shared.sync(); await load() } })
        .task { await load() }
        .task { await learn() }
        .fullScreenCover(item: $open, onDismiss: { Task { await load() } }) { v in Viewer(items: v.items, start: v.start, talk: talks ? album : nil) }
    }

    private func load() async { items = await Library.shared.items(inAlbum: album.id) }

    /// Whether there's anyone to talk to in it: shared with you, or by you (the sync feed doesn't say).
    private func learn() async {
        if album.role != "owner" { talks = true; return }
        guard let api = Library.shared.api(), let info = try? await api.albumInfo(album.id) else { return }
        talks = info.talks
    }
}

/// Tiles for a list of items (an album, search results), choosable.
struct TileList: View {
    var items: [Item]
    var selection: Selection
    var frames: TileFrames
    var lock: Binding<Bool>
    var open: (Item) -> Void

    var body: some View {
        Tiles {
            ForEach(Array(items.enumerated()), id: \.element.id) { i, item in
                Tile(item: item).choosable(item, at: i, selection, frames, open: open)
            }
        }
        .dragToChoose(selection, frames, lock: lock) { i in items.indices.contains(i) ? items[i] : nil }
    }
}

/// Stars, a flag and a color label, small, on a tile (when Ratings & flags is on).
struct Marks: View {
    var item: Item

    var body: some View {
        HStack(spacing: 3) {
            if !item.label.isEmpty { Circle().fill(labelColor(item.label)).frame(width: 8, height: 8) }
            if item.flag == "pick" { Image(systemName: "flag.fill") }
            if item.flag == "reject" { Image(systemName: "xmark.circle.fill") }
            if item.rating > 0 { Text("\(item.rating)★").monospacedDigit() }
        }
        .font(.caption2.weight(.bold)).foregroundStyle(.white).shadow(radius: 2)
        .padding(.horizontal, 4).padding(.vertical, 2)
        .background(.black.opacity(0.35), in: Capsule())
    }
}

func labelColor(_ name: String) -> Color {
    switch name {
    case "red": .red
    case "yellow": .yellow
    case "green": .green
    case "blue": .blue
    case "purple": .purple
    default: .gray
    }
}

/// What the Library shows: all items, photos, videos or favorites; and, in a Household with
/// others, everyone's, yours or what's shared with it.
struct FilterMenu: View {
    @Environment(Library.self) private var library

    var body: some View {
        @Bindable var library = library
        Menu {
            Picker("Show", selection: $library.show) {
                Label("All items", systemImage: "photo.on.rectangle").tag(Show.all)
                Label("Photos", systemImage: "photo").tag(Show.photos)
                Label("Videos", systemImage: "video").tag(Show.videos)
                Label("Favorites", systemImage: "heart").tag(Show.favorites)
            }
            if library.inHousehold {
                Picker("Whose", selection: $library.whose) {
                    Label("Everyone's", systemImage: "person.2").tag(Whose.everyone)
                    Label("Mine", systemImage: "person").tag(Whose.mine)
                    Label("The Household's", systemImage: "house").tag(Whose.household)
                }
            }
        } label: {
            Label("Filter", systemImage: filtered ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle")
        }
        .accessibilityIdentifier("filter")
    }

    private var filtered: Bool { library.show != .all || library.whose != .everyone }
}

/// Under the title while a filter's on: what it shows, and a tap clears it.
struct FilterPill: View {
    @Environment(Library.self) private var library

    var body: some View {
        if library.show != .all || library.whose != .everyone {
            Button {
                library.show = .all
                library.whose = .everyone
            } label: {
                HStack(spacing: 4) {
                    Text(said)
                    Image(systemName: "xmark").font(.caption2.weight(.bold))
                }
                .font(.footnote.weight(.medium))
                .padding(.horizontal, 10).padding(.vertical, 5)
                .background(.tint.opacity(0.15), in: Capsule())
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("filter-pill")
        }
    }

    private var said: String {
        let show = switch library.show {
        case .all: ""
        case .photos: "Photos"
        case .videos: "Videos"
        case .favorites: "Favorites"
        }
        let whose = switch library.whose {
        case .everyone: ""
        case .mine: "Mine"
        case .household: "The Household's"
        }
        return [show, whose].filter { !$0.isEmpty }.joined(separator: " · ")
    }
}

/// Memories across the top of the Library: a day in earlier years, or someone; a tap shows its items.
struct MemoriesStrip: View {
    var memories: [Memory]
    var open: (Memory) -> Void

    var body: some View {
        ScrollView(.horizontal) {
            LazyHStack(spacing: 10) {
                ForEach(memories) { m in
                    Button { open(m) } label: { MemoryCard(memory: m) }.buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 16)
        }
        .scrollIndicators(.hidden)
        .frame(height: 190)
        .accessibilityIdentifier("memories")
    }
}

private struct MemoryCard: View {
    var memory: Memory

    var body: some View {
        Color.clear
            .frame(width: 140, height: 190)
            .overlay {
                if let cover = memory.assets.first { ItemPicture(item: cover.item) } else { Rectangle().fill(.quaternary) }
            }
            .overlay(alignment: .bottomLeading) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(memory.title).font(.subheadline.weight(.bold)).lineLimit(2)
                    if !memory.subtitle.isEmpty { Text(memory.subtitle).font(.caption).lineLimit(2).opacity(0.85) }
                }
                .foregroundStyle(.white)
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(LinearGradient(colors: [.clear, .black.opacity(0.6)], startPoint: .top, endPoint: .bottom))
                .dynamicTypeSize(...DynamicTypeSize.xxLarge) // a card of fixed size: past this its words were cut off
            }
            .clipShape(RoundedRectangle(cornerRadius: 14))
            .accessibilityElement(children: .combine)
    }
}
