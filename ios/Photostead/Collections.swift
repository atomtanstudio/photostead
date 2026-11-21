// Collections: everything other than the timeline, on one page. Albums (yours and the ones shared
// with you), then people, places and AI characters, what arrived lately, favorites, videos, what
// was made with AI (and its prompts), what Photostead Capture saved, the labels Photostead sorts
// things into, and the archive, trash and vault. Lists the phone has (the sync feed's) open at
// once; the rest come from the server a month at a time as they're scrolled to.

import SwiftUI

/// Where Collections (and Search) go.
enum Route: Hashable {
    case album(AlbumRow)
    case allAlbums
    case local(title: String, which: String)
    case remote(title: String, collection: String, trash: Bool = false)
    case people
    case person(Person)
    case places
    case place(PlaceRow)
    case characters
    case character(CharacterRow)
    case prompts
    case prompt(PromptRow)
    case recent
    case month(String) // YYYY-MM
    case vault
}

struct CollectionsView: View {
    @Environment(Library.self) private var library
    @State private var naming = false
    @State private var newTitle = ""
    @State private var opening: Route? // an album, opened from its card (a link in a list row would draw a chevron beside it)

    private var browse: Browse { Browse.shared }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    if library.albums.isEmpty {
                        NoAlbums { newTitle = ""; naming = true }
                    } else {
                        LazyVGrid(columns: [GridItem(.adaptive(minimum: 150), spacing: 14)], spacing: 16) {
                            ForEach(library.albums.prefix(4)) { album in
                                Button { opening = .album(album) } label: { AlbumCard(album: album) }.buttonStyle(.plain)
                            }
                        }
                        .padding(.vertical, 6)
                        if library.albums.count > 4 {
                            NavigationLink(value: Route.allAlbums) { Text("All albums").badge(library.albums.count) }
                        }
                    }
                } header: {
                    HStack {
                        Text("Albums")
                        Spacer()
                        Button { newTitle = ""; naming = true } label: { Image(systemName: "plus") }
                            .accessibilityLabel("New album")
                            .accessibilityIdentifier("new-album")
                    }
                }
                Section {
                    row("People", "person.2", library.people.isEmpty ? nil : library.people.count, .people)
                    row("Places", "mappin.and.ellipse", browse.places.isEmpty ? nil : browse.places.count, .places)
                    if !browse.characters.isEmpty {
                        row("Characters", "sparkles", browse.characters.count, .characters)
                    }
                }
                Section {
                    row("Recently added", "clock", nil, .recent)
                    row("Favorites", "heart", library.counts?.favorites, .local(title: "Favorites", which: "favorites"))
                    row("Videos", "video", library.counts?.videos, .local(title: "Videos", which: "videos"))
                    row("Made with AI", "wand.and.stars", library.counts?.ai, .remote(title: "Made with AI", collection: "ai"))
                    if browse.prompts > 0 {
                        row("Prompts", "text.quote", browse.prompts, .prompts)
                    }
                    row("Captured by me", "camera", library.counts?.captured, .remote(title: "Captured by me", collection: "captured"))
                }
                let labels = utilityLabels.filter { (library.counts?.labels[$0.name] ?? 0) > 0 }
                if !labels.isEmpty {
                    Section("Sorted for you") {
                        ForEach(labels, id: \.name) { l in
                            row(l.title, l.symbol, library.counts?.labels[l.name], .remote(title: l.title, collection: l.name))
                        }
                    }
                }
                Section {
                    row("Archive", "archivebox", library.counts?.archived, .local(title: "Archive", which: "archive"))
                    row("Trash", "trash", library.counts?.trash, .remote(title: "Trash", collection: "trash", trash: true))
                    row("Vault", "lock", nil, .vault)
                }
            }
            .navigationTitle("Collections")
            .toolbar { ToolbarItem(placement: .topBarTrailing) { AccountButton() } }
            .navigationDestination(for: Route.self) { route in destination(route) }
            .navigationDestination(item: $opening) { destination($0) }
            .refreshable {
                await library.sync()
                await library.refreshExtras()
                await browse.refresh(force: true)
            }
            .task {
                await library.refreshExtras()
                await browse.refresh()
            }
            .alert("New album", isPresented: $naming) {
                TextField("Title", text: $newTitle)
                Button("Make it") { Task { try? await library.createAlbum(newTitle.trimmingCharacters(in: .whitespaces)) } }
                    .disabled(newTitle.trimmingCharacters(in: .whitespaces).isEmpty)
                Button("Cancel", role: .cancel) {}
            }
        }
    }

    private func row(_ title: String, _ symbol: String, _ count: Int?, _ route: Route) -> some View {
        NavigationLink(value: route) {
            Label {
                HStack {
                    Text(title)
                    Spacer()
                    if let count { Text(count.formatted()).foregroundStyle(.secondary).monospacedDigit() }
                }
            } icon: { Image(systemName: symbol) }
        }
        .accessibilityIdentifier("collection-\(title)")
    }
}

@ViewBuilder
func destination(_ route: Route) -> some View {
    switch route {
    case .album(let album):
        // A smart album's items are whatever its rules pick now, so they come from the server.
        if album.kind == "smart" { RemoteGrid(title: album.title, collection: "album:\(album.id)", empty: "Nothing matches this smart album yet.") } else { AlbumView(album: album) }
    case .allAlbums: AllAlbums()
    case .local(let title, let which): LocalList(title: title, which: which)
    case .remote(let title, let collection, let trash): RemoteGrid(title: title, collection: collection, trash: trash)
    case .people: PeopleView()
    case .person(let person): PersonView(person: person)
    case .places: PlacesView()
    case .place(let place): RemoteGrid(title: place.name, collection: "place:\(place.id)")
    case .characters: CharactersView()
    case .character(let c): RemoteGrid(title: c.name, collection: "character:\(c.id)")
    case .prompts: PromptsView()
    case .prompt(let p): RemoteGrid(title: p.text, collection: "prompt:\(p.id)")
    case .recent: RecentView()
    case .month(let month): MonthView(month: month)
    case .vault: VaultView()
    }
}

/// Albums, before there are any: what they're for, and making one here (or from chosen items).
private struct NoAlbums: View {
    var make: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("No albums yet").font(.headline)
            Text("Make one here, or choose photos in Library and tap Add to album.")
                .font(.subheadline).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Button(action: make) { Label("New album", systemImage: "plus") }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("first-album")
        }
        .padding(.vertical, 6)
    }
}

private struct AllAlbums: View {
    @Environment(Library.self) private var library

    var body: some View {
        ScrollView {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 150), spacing: 14)], alignment: .leading, spacing: 20) {
                ForEach(library.albums) { album in
                    NavigationLink(value: Route.album(album)) { AlbumCard(album: album) }.buttonStyle(.plain)
                }
            }
            .padding(16)
        }
        .navigationTitle("Albums")
    }
}

/// A list the phone has: favorites, videos, the archive.
private struct LocalList: View {
    var title: String
    var which: String
    @Environment(Library.self) private var library
    @State private var months: [Month] = []
    @State private var items: [Item] = []
    @State private var open: Viewing?
    @State private var selection = Selection()
    @State private var frames = TileFrames()
    @State private var lock = false

    var body: some View {
        ScrollView {
            if items.isEmpty {
                Text("Nothing here.").foregroundStyle(.secondary).padding(60)
            } else {
                Grid(months: months, columns: 4, selection: selection, frames: frames, lock: $lock) { item in open = Viewing(items: items, start: item.id) }
            }
        }
        .scrollDisabled(lock)
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if !selection.on && !items.isEmpty { ToolbarItem(placement: .topBarTrailing) { Button("Select") { selection.start() } } }
        }
        .choosing(selection) { await load() }
        .task(id: library.timeline.count) { await load() }
        .fullScreenCover(item: $open, onDismiss: { Task { await load() } }) { v in Viewer(items: v.items, start: v.start) }
    }

    private func load() async {
        items = await library.list(which)
        months = Library.group(items)
    }
}

/// A collection from the server, a month at a time as it's scrolled to.
struct RemoteGrid: View {
    var title: String
    var collection: String
    var trash = false
    var empty = "Nothing here."
    @Environment(Library.self) private var library
    @State private var buckets: [Bucket]?
    @State private var loaded: [String: [Item]] = [:]
    @State private var open: Viewing?
    @State private var problem = ""
    @State private var selection = Selection()
    @State private var frames = TileFrames()
    @State private var lock = false

    private var place: Place { trash ? .trash : collection == "vault" ? .vault : .library }

    /// Where each month's first item is in the whole collection (to choose a run across months).
    private var starts: [String: Int] {
        var at = 0
        var out: [String: Int] = [:]
        for b in buckets ?? [] {
            out[b.month] = at
            at += b.count
        }
        return out
    }

    var body: some View {
        ScrollView {
            if let buckets, buckets.isEmpty {
                Text(trash ? "The trash is empty." : empty).foregroundStyle(.secondary).multilineTextAlignment(.center).padding(60)
            } else if let buckets {
                if trash {
                    Text("Items here are deleted for good 30 days after they were put here.").font(.footnote).foregroundStyle(.secondary).padding(.horizontal, 16).padding(.top, 8)
                }
                let starts = starts
                Tiles(pinned: [.sectionHeaders]) {
                    ForEach(buckets, id: \.month) { b in
                        Section {
                            if let items = loaded[b.month] {
                                ForEach(Array(items.enumerated()), id: \.element.id) { i, item in
                                    Tile(item: item).choosable(item, at: (starts[b.month] ?? 0) + i, selection, frames) { openAt($0) }
                                }
                            } else {
                                // Named by month: the same numbers in every month were one set of views
                                // to the grid, so only the first month's asked for its items.
                                ForEach((0..<min(b.count, 400)).map { "\(b.month)/\($0)" }, id: \.self) { _ in
                                    Rectangle().fill(.quaternary).aspectRatio(1, contentMode: .fit)
                                        .onAppear { Task { await load(b.month) } }
                                }
                            }
                        } header: {
                            MonthHeader(title: monthTitle(b.month), count: b.count)
                        }
                    }
                }
                .dragToChoose(selection, frames, lock: $lock) { i in
                    guard let b = buckets.last(where: { (starts[$0.month] ?? 0) <= i }), let items = loaded[b.month] else { return nil }
                    let at = i - (starts[b.month] ?? 0)
                    return items.indices.contains(at) ? items[at] : nil
                }
            } else {
                ProgressView().padding(60)
                if !problem.isEmpty { Text(problem).foregroundStyle(.secondary).padding() }
            }
        }
        .scrollDisabled(lock)
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if !selection.on && !(buckets ?? []).isEmpty { ToolbarItem(placement: .topBarTrailing) { Button("Select") { selection.start() } } }
        }
        .choosing(selection, in: place) { await reload() }
        .task { await reload() }
        .refreshable { await reload() }
        .fullScreenCover(item: $open, onDismiss: { Task { await reload() } }) { v in Viewer(items: v.items, start: v.start, trash: trash, vault: place == .vault) }
    }

    private func openAt(_ item: Item) {
        let all = (buckets ?? []).flatMap { loaded[$0.month] ?? [] }
        open = Viewing(items: all, start: item.id)
    }

    private func reload() async {
        guard let api = library.api() else { return }
        do {
            let b = try await api.buckets(collection)
            loaded = [:]
            buckets = b
            if let first = b.first { await load(first.month) }
        } catch {
            problem = error.localizedDescription
        }
    }

    private func load(_ month: String) async {
        guard loaded[month] == nil, let api = library.api() else { return }
        if let items = try? await api.month(month, of: collection) { loaded[month] = items }
    }
}

struct MonthHeader: View {
    var title: String
    var count: Int

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(title).font(.title3.weight(.semibold))
            Text(count.formatted()).font(.footnote.monospacedDigit()).foregroundStyle(.secondary)
            Spacer()
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
        .background(.bar, ignoresSafeAreaEdges: .vertical)
    }
}

func monthTitle(_ key: String) -> String {
    let parse = DateFormatter()
    parse.dateFormat = "yyyy-MM"
    parse.locale = Locale(identifier: "en_US_POSIX")
    let show = DateFormatter()
    show.setLocalizedDateFormatFromTemplate("MMMM yyyy")
    return parse.date(from: String(key.prefix(7))).map(show.string(from:)) ?? key
}

// MARK: People

struct PeopleView: View {
    @Environment(Library.self) private var library

    var body: some View {
        ScrollView {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 96), spacing: 16)], spacing: 18) {
                ForEach(library.people) { person in face(person) }
            }
            .padding(16)
            if !library.unnamed.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Not named yet").font(.title3.weight(.semibold))
                    Text("Found by face recognition. Name someone to find them in Search and in Memories.").font(.footnote).foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 16).padding(.top, 8)
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 72), spacing: 12)], spacing: 14) {
                    ForEach(library.unnamed.prefix(120)) { person in face(person, size: 64) }
                }
                .padding(16)
            }
        }
        .navigationTitle("People")
        .task { await library.refreshExtras() }
    }

    private func face(_ person: Person, size: CGFloat = 88) -> some View {
        NavigationLink(value: Route.person(person)) {
            VStack(spacing: 6) {
                FacePicture(person: person).frame(width: size, height: size).clipShape(Circle())
                if !person.name.isEmpty { Text(person.name).font(.footnote.weight(.medium)).lineLimit(1) }
                Text(person.count.formatted()).font(.caption2).foregroundStyle(.secondary)
            }
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier(person.name.isEmpty ? "unnamed-person" : "person")
    }
}

/// Someone's items, and naming them: a new name, or the name of someone already named (then
/// they're the same person, and become one).
struct PersonView: View {
    var person: Person
    @Environment(Library.self) private var library
    @Environment(\.dismiss) private var dismiss
    @State private var name: String
    @State private var naming = false
    @State private var typed = ""
    @State private var same: Person?
    @State private var problem = ""

    init(person: Person) {
        self.person = person
        _name = State(initialValue: person.name)
    }

    var body: some View {
        RemoteGrid(title: name.isEmpty ? "Not named yet" : name, collection: "person:\(person.id)")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button(name.isEmpty ? "Name" : "Rename") { typed = name; naming = true }.accessibilityIdentifier("name-person")
                }
            }
            .alert(name.isEmpty ? "Who is this?" : "Their name", isPresented: $naming) {
                TextField("Name", text: $typed).textInputAutocapitalization(.words)
                Button("Save") { save() }
                Button("Cancel", role: .cancel) {}
            }
            .confirmationDialog("Is this \(same?.name ?? "")?", isPresented: Binding(get: { same != nil }, set: { if !$0 { same = nil } }), titleVisibility: .visible) {
                Button("Yes, the same person") { Task { await merge() } }
                Button("No, someone else") { Task { await rename(typed) } }
            } message: {
                Text("Someone is named that already. If it's them, these faces and items join theirs.")
            }
            .alert("Couldn’t", isPresented: Binding(get: { !problem.isEmpty }, set: { if !$0 { problem = "" } })) {
                Button("OK") {}
            } message: { Text(problem) }
    }

    private func save() {
        let new = typed.trimmingCharacters(in: .whitespaces)
        guard !new.isEmpty, new != name else { return }
        if let other = library.people.first(where: { $0.id != person.id && $0.name.caseInsensitiveCompare(new) == .orderedSame }) {
            same = other
        } else {
            Task { await rename(new) }
        }
    }

    private func rename(_ new: String) async {
        do {
            try await library.api()?.name(person: person.id, new)
            name = new
            await library.refreshExtras()
        } catch {
            problem = error.localizedDescription
        }
    }

    private func merge() async {
        guard let into = same else { return }
        do {
            try await library.api()?.merge(person: person.id, into: into.id)
            await library.refreshExtras()
            dismiss()
        } catch {
            problem = error.localizedDescription
        }
    }
}

/// A person's face (from the server), with their cover's placeholder until it arrives.
struct FacePicture: View {
    var person: Person
    @State private var image: UIImage?

    var body: some View {
        ZStack {
            if let image {
                Image(uiImage: image).resizable().scaledToFill()
            } else if let placeholder = Images.shared.placeholder(person.coverThumbhash) {
                Image(uiImage: placeholder).resizable().scaledToFill()
            } else {
                Circle().fill(.quaternary).overlay { Image(systemName: "person.fill").foregroundStyle(.secondary) }
            }
        }
        .task(id: person.id) {
            guard let base = Library.shared.base, let token = Library.shared.token else { return }
            if let face = person.coverFace {
                image = await Images.shared.picture(path: "api/v1/faces/\(face)/thumbnail", key: "face/\(face)", base: base, token: token)
            } else if let cover = person.cover { // no face found yet (someone from an import): their item, not a blur
                image = await Images.shared.image(cover, "thumbnail", version: Images.version(person.coverThumbhash), base: base, token: token)
            }
        }
    }
}
