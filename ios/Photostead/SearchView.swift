// Search: one box. Search finds by meaning ("dog on the beach at sunset"), by name, and by text in
// the picture; Ask answers questions ("photos of Mia from last summer"). Before typing, things to
// tap, as Photos has them: people and pets, places, characters and recent months (each opens at
// once), kinds of things search by meaning finds well (each runs the search), the labels, and
// recent searches. Both need the server (they run there).

import SwiftUI

struct SearchView: View {
    @Environment(Library.self) private var library
    @State private var text = ""
    @State private var results: [Item]?
    @State private var answer: Answer?
    @State private var namesOnly = false
    @State private var busy = false
    @State private var problem = ""
    @State private var open: Viewing?
    @State private var selection = Selection()
    @State private var frames = TileFrames()
    @State private var lock = false
    @AppStorage("recentSearches") private var recentJSON = "[]"

    private var recent: [String] { (try? JSONDecoder().decode([String].self, from: Data(recentJSON.utf8))) ?? [] }

    var body: some View {
        NavigationStack {
            Group {
                if busy {
                    ProgressView(text.isEmpty ? "Searching…" : "Looking for “\(text)”…").frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if let results {
                    Results(items: results, answer: answer, problem: problem, namesOnly: namesOnly, selection: selection, frames: frames, lock: $lock) { item in
                        open = Viewing(items: results, start: item.id)
                    }
                } else if !text.isEmpty {
                    List {
                        Button { Task { await search() } } label: { Label("Search for “\(text)”", systemImage: "magnifyingglass") }
                            .accessibilityIdentifier("search-for")
                        Button { Task { await ask() } } label: { Label("Ask “\(text)”", systemImage: "sparkles") }
                            .accessibilityIdentifier("ask")
                    }
                } else {
                    Suggestions(recent: recent, clear: { recentJSON = "[]" }) { q in
                        text = q
                        Task { await search() }
                    }
                }
            }
            .navigationTitle("Search")
            .toolbar {
                if !selection.on {
                    if let results, !results.isEmpty {
                        ToolbarItem(placement: .topBarTrailing) { Button("Select") { selection.start() } }
                    }
                    ToolbarItem(placement: .topBarTrailing) { AccountButton() }
                }
            }
            .choosing(selection)
            .navigationDestination(for: Route.self) { destination($0) }
            .searchable(text: $text, placement: .navigationBarDrawer(displayMode: .always), prompt: "Search or ask")
            .onSubmit(of: .search) { Task { await search() } }
            .onChange(of: text) { if text.isEmpty { results = nil; answer = nil; problem = ""; namesOnly = false } }
            .task {
                if library.people.isEmpty { await library.refreshExtras() }
                await Browse.shared.refresh()
            }
        }
        .fullScreenCover(item: $open) { v in Viewer(items: v.items, start: v.start) }
    }

    private func remember(_ q: String) {
        var list = recent.filter { $0.caseInsensitiveCompare(q) != .orderedSame }
        list.insert(q, at: 0)
        if let data = try? JSONEncoder().encode(Array(list.prefix(8))) { recentJSON = String(decoding: data, as: UTF8.self) }
    }

    private func search() async {
        let q = text.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty, let api = library.api() else { return }
        busy = true
        defer { busy = false }
        answer = nil
        problem = ""
        do {
            let found = try await api.find(q)
            results = found.items
            namesOnly = !found.byMeaning
            remember(q)
        } catch {
            results = []
            namesOnly = false
            problem = error.localizedDescription
        }
    }

    private func ask() async {
        let q = text.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty, let api = library.api() else { return }
        busy = true
        defer { busy = false }
        problem = ""
        namesOnly = false
        do {
            let a = try await api.ask(q)
            answer = a
            results = a.assets.map(\.item)
            remember(q)
        } catch {
            answer = nil
            results = []
            problem = error.localizedDescription
        }
    }
}

/// Before typing: what to tap. People, places, characters and months open their items; things to
/// try and recent searches run a search; the labels open their collections. While the box is
/// being typed in (and still empty), recent searches come first, as in Photos.
private struct Suggestions: View {
    var recent: [String]
    var clear: () -> Void
    var pick: (String) -> Void
    @Environment(Library.self) private var library
    @Environment(\.isSearching) private var searching
    @ScaledMetric(relativeTo: .body) private var chipWidth: CGFloat = 150
    // Faces and covers grow with the text under them, up to a point, so names aren't cut short.
    @ScaledMetric(relativeTo: .caption) private var faceSize: CGFloat = 72
    @ScaledMetric(relativeTo: .caption) private var coverSize: CGFloat = 104
    @State private var opening: Route? // a label, opened from its chip (a link there would draw a chevron)

    private var face: CGFloat { min(faceSize, 128) }
    private var cover: CGFloat { min(coverSize, 168) }

    private var browse: Browse { Browse.shared }

    var body: some View {
        List {
            if searching { recents }
            people
            places
            characters
            months
            things
            labels
            if !searching { recents }
            Section {} footer: {
                Text("Search by what's in a picture, by its name, or by words in it. Ask answers questions, like “photos of Mia from last summer”.")
            }
        }
        .navigationDestination(item: $opening) { destination($0) }
    }

    // MARK: Opening at once

    @ViewBuilder private var people: some View {
        if !library.people.isEmpty {
            Section {
                Carousel {
                    ForEach(library.people.prefix(20)) { person in
                        NavigationLink(value: Route.person(person)) {
                            VStack(spacing: 6) {
                                FacePicture(person: person).frame(width: face, height: face).clipShape(Circle())
                                Text(person.name).font(.caption.weight(.medium)).lineLimit(2).multilineTextAlignment(.center)
                                    .frame(width: face + 8, alignment: .top)
                            }
                        }
                        .buttonStyle(.plain)
                        .accessibilityElement(children: .ignore)
                        .accessibilityLabel(person.name)
                        .accessibilityValue(items(person.count))
                        .accessibilityAddTraits(.isButton)
                        .accessibilityIdentifier("suggest-person")
                    }
                }
            } header: {
                Header(title: library.people.contains { $0.kind == "pet" } ? "People & Pets" : "People", all: .people, id: "people")
            }
        }
    }

    @ViewBuilder private var places: some View {
        if !browse.places.isEmpty {
            Section {
                Carousel {
                    ForEach(browse.places.sorted { $0.count > $1.count }.prefix(20)) { place in
                        NavigationLink(value: Route.place(place)) {
                            VStack(alignment: .leading, spacing: 4) {
                                CoverPicture(id: place.cover, hash: place.coverThumbhash, symbol: "mappin")
                                    .frame(width: cover, height: cover)
                                    .clipShape(RoundedRectangle(cornerRadius: 14))
                                Text(place.name).font(.caption.weight(.semibold)).lineLimit(2)
                                Text(items(place.count)).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                            }
                            .frame(width: cover, alignment: .leading)
                        }
                        .buttonStyle(.plain)
                        .accessibilityElement(children: .ignore)
                        .accessibilityLabel(place.name)
                        .accessibilityValue(place.line)
                        .accessibilityAddTraits(.isButton)
                        .accessibilityIdentifier("suggest-place")
                    }
                }
            } header: {
                Header(title: "Places", all: .places, id: "places")
            }
        }
    }

    @ViewBuilder private var characters: some View {
        if !browse.characters.isEmpty {
            Section {
                Carousel {
                    ForEach(browse.characters.prefix(20)) { c in
                        NavigationLink(value: Route.character(c)) {
                            VStack(spacing: 6) {
                                CoverPicture(id: c.samples.first?.id, hash: c.samples.first?.thumbhash, symbol: "sparkles")
                                    .frame(width: face, height: face).clipShape(Circle())
                                Text(c.name).font(.caption.weight(.medium)).lineLimit(2).multilineTextAlignment(.center)
                                    .frame(width: face + 8, alignment: .top)
                            }
                        }
                        .buttonStyle(.plain)
                        .accessibilityElement(children: .ignore)
                        .accessibilityLabel(c.name)
                        .accessibilityValue(items(c.count))
                        .accessibilityAddTraits(.isButton)
                        .accessibilityIdentifier("suggest-character")
                    }
                }
            } header: {
                Header(title: "Characters", all: browse.characters.count > 4 ? .characters : nil, id: "characters")
            }
        }
    }

    @ViewBuilder private var months: some View {
        if !browse.months.isEmpty {
            Section {
                Carousel(spacing: 10) {
                    ForEach(browse.months.prefix(6), id: \.month) { b in
                        NavigationLink(value: Route.month(b.month)) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(monthPart(b.month, "MMMM")).font(.subheadline.weight(.semibold))
                                Text("\(monthPart(b.month, "yyyy")) · \(b.count.formatted())").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                            }
                            .padding(.horizontal, 14).padding(.vertical, 10)
                            .frame(minWidth: 112, alignment: .leading)
                            .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 12))
                        }
                        .buttonStyle(.plain)
                        .accessibilityElement(children: .ignore)
                        .accessibilityLabel(monthTitle(b.month))
                        .accessibilityValue(items(b.count))
                        .accessibilityAddTraits(.isButton)
                        .accessibilityIdentifier("suggest-month")
                    }
                }
            } header: {
                Header(title: "Recent months")
            }
        }
    }

    // MARK: Running a search

    /// Only once search by meaning has looked at the library: by names alone, "beach" finds little.
    @ViewBuilder private var things: some View {
        if (browse.meaning?.embedded ?? 0) > 0 {
            Section {
                Chips(width: chipWidth) {
                    ForEach(thingsToTry, id: \.q) { t in
                        Button { pick(t.q) } label: { Chip(title: t.title, symbol: t.symbol) }
                            .buttonStyle(.plain)
                            .accessibilityLabel(t.title)
                            .accessibilityHint("Searches for it")
                            .accessibilityIdentifier("suggest-thing")
                    }
                }
            } header: {
                Header(title: "Things in pictures")
            }
        }
    }

    @ViewBuilder private var labels: some View {
        let labels = utilityLabels.filter { (library.counts?.labels[$0.name] ?? 0) > 0 }
        if !labels.isEmpty {
            Section {
                Chips(width: chipWidth) {
                    ForEach(labels, id: \.name) { l in
                        Button { opening = .remote(title: l.title, collection: l.name) } label: {
                            Chip(title: l.title, symbol: l.symbol, count: library.counts?.labels[l.name])
                        }
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("suggest-label")
                    }
                }
            } header: {
                Header(title: "Sorted for you")
            }
        }
    }

    @ViewBuilder private var recents: some View {
        if !recent.isEmpty {
            Section {
                ForEach(recent, id: \.self) { q in
                    Button { pick(q) } label: { Label(q, systemImage: "clock.arrow.circlepath") }
                        .foregroundStyle(.primary)
                        .accessibilityIdentifier("recent-search")
                }
            } header: {
                HStack {
                    Text("Recent")
                    Spacer()
                    Button("Clear", action: clear)
                        .font(.subheadline)
                        .accessibilityLabel("Clear recent searches")
                        .accessibilityIdentifier("clear-recent")
                }
            }
        }
    }

    private func items(_ n: Int) -> String { n == 1 ? "1 item" : "\(n.formatted()) items" }

    private func monthPart(_ key: String, _ template: String) -> String {
        let parse = DateFormatter()
        parse.dateFormat = "yyyy-MM"
        parse.locale = Locale(identifier: "en_US_POSIX")
        let show = DateFormatter()
        show.setLocalizedDateFormatFromTemplate(template)
        return parse.date(from: String(key.prefix(7))).map(show.string(from:)) ?? key
    }
}

/// A suggestions section's title, and See all when there's a whole page of them.
private struct Header: View {
    var title: String
    var all: Route?
    var id = ""

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(title)
            Spacer()
            if let all {
                NavigationLink("See all", value: all)
                    .font(.subheadline)
                    .accessibilityLabel("See all \(title.lowercased())")
                    .accessibilityIdentifier("see-all-\(id)")
            }
        }
    }
}

/// A row that scrolls sideways, edge to edge, on the page's own background.
private struct Carousel<Content: View>: View {
    var spacing: CGFloat = 14
    @ViewBuilder var content: () -> Content

    var body: some View {
        ScrollView(.horizontal) {
            LazyHStack(alignment: .top, spacing: spacing, content: content)
                .padding(.horizontal, 20).padding(.vertical, 2)
        }
        .scrollIndicators(.hidden)
        .listRowInsets(EdgeInsets())
        .listRowBackground(Color.clear)
    }
}

/// Chips in as many columns as fit (one, at the largest text sizes).
private struct Chips<Content: View>: View {
    var width: CGFloat
    @ViewBuilder var content: () -> Content

    var body: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: width), spacing: 10)], spacing: 10, content: content)
            .listRowInsets(EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 20))
            .listRowBackground(Color.clear)
    }
}

/// A thing to search for, or a label (with how many, under it).
private struct Chip: View {
    var title: String
    var symbol: String
    var count: Int?
    @ScaledMetric(relativeTo: .body) private var symbolWidth: CGFloat = 28 // the titles line up, whatever the symbol's width

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: symbol).foregroundStyle(.secondary).frame(width: symbolWidth)
            VStack(alignment: .leading, spacing: 1) {
                Text(title).lineLimit(1).minimumScaleFactor(0.8) // "Screenshots" whole, in two columns
                if let count { Text(count.formatted()).font(.footnote.monospacedDigit()).foregroundStyle(.secondary) }
            }
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
        .padding(.horizontal, 12).padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 12))
        .contentShape(RoundedRectangle(cornerRadius: 12))
    }
}

/// What a search or a question found: the answer (for a question), then the items.
private struct Results: View {
    var items: [Item]
    var answer: Answer?
    var problem: String
    var namesOnly: Bool
    var selection: Selection
    var frames: TileFrames
    var lock: Binding<Bool>
    var open: (Item) -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 6) {
                if let answer {
                    Text(answer.answer.isEmpty ? answer.describe : answer.answer).font(.headline)
                    if !answer.answer.isEmpty && !answer.describe.isEmpty { Text(answer.describe).font(.callout).foregroundStyle(.secondary) }
                    ForEach(answer.notes ?? [], id: \.self) { Text($0).font(.footnote).foregroundStyle(.secondary) }
                }
                if !problem.isEmpty {
                    Text(problem).foregroundStyle(.secondary)
                } else {
                    Text(items.isEmpty ? "Nothing found." : "\(items.count.formatted()) found").font(.footnote).foregroundStyle(.secondary)
                        .accessibilityIdentifier("found")
                    if namesOnly {
                        Text("Search by meaning isn't running on Photostead right now, so this looked at names only.")
                            .font(.footnote).foregroundStyle(.secondary)
                            .accessibilityIdentifier("names-only")
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 16).padding(.vertical, 10)
            TileList(items: items, selection: selection, frames: frames, lock: lock, open: open)
        }
        .scrollDisabled(lock.wrappedValue)
    }
}
