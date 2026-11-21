// Recently added (what arrived lately, newest-arrived first: backed up, uploaded, imported or found
// in a folder, whenever it was taken) and a single month of the timeline (from Search's months).
// Both from the server, so they're the same as the web app's.

import SwiftUI

struct RecentView: View {
    @Environment(Library.self) private var library
    @State private var items: [Item]?
    @State private var next: String?
    @State private var loading = false
    @State private var problem = ""
    @State private var open: Viewing?
    @State private var selection = Selection()
    @State private var frames = TileFrames()
    @State private var lock = false

    var body: some View {
        ScrollView {
            if let items {
                if items.isEmpty {
                    ContentUnavailableView("Nothing here yet", systemImage: "clock",
                                           description: Text("Photos and videos show here as they arrive: backed up, uploaded or imported."))
                        .padding(.top, 60)
                } else {
                    TileList(items: items, selection: selection, frames: frames, lock: $lock) { item in open = Viewing(items: items, start: item.id) }
                    if next != nil {
                        ProgressView().padding(30).onAppear { Task { await more() } }
                    }
                }
            } else if !problem.isEmpty {
                Text(problem).foregroundStyle(.secondary).padding(40)
            } else {
                ProgressView().padding(60)
            }
        }
        .scrollDisabled(lock)
        .navigationTitle("Recently added")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if !selection.on && !(items ?? []).isEmpty { ToolbarItem(placement: .topBarTrailing) { Button("Select") { selection.start() } } }
        }
        .choosing(selection) { await reload() }
        .task { await reload() }
        .refreshable { await reload() }
        .fullScreenCover(item: $open, onDismiss: { Task { await reload() } }) { v in Viewer(items: v.items, start: v.start) }
    }

    private func reload() async {
        guard let api = library.api() else { return }
        do {
            let page = try await api.recent()
            items = page.items
            next = page.next
            problem = ""
        } catch {
            if items == nil { problem = error.localizedDescription }
        }
    }

    /// The next page, as the end comes into view.
    private func more() async {
        guard !loading, let cursor = next, let api = library.api() else { return }
        loading = true
        defer { loading = false }
        guard let page = try? await api.recent(after: cursor) else { return }
        let have = Set((items ?? []).map(\.id))
        items = (items ?? []) + page.items.filter { !have.contains($0.id) }
        next = page.next
    }
}

/// One month of the timeline, as the server has it (whatever the Library tab is showing).
struct MonthView: View {
    var month: String // YYYY-MM
    @Environment(Library.self) private var library
    @State private var items: [Item]?
    @State private var problem = ""
    @State private var open: Viewing?
    @State private var selection = Selection()
    @State private var frames = TileFrames()
    @State private var lock = false

    var body: some View {
        ScrollView {
            if let items {
                if items.isEmpty {
                    Text("Nothing here.").foregroundStyle(.secondary).padding(60)
                } else {
                    TileList(items: items, selection: selection, frames: frames, lock: $lock) { item in open = Viewing(items: items, start: item.id) }
                }
            } else if !problem.isEmpty {
                Text(problem).foregroundStyle(.secondary).padding(40)
            } else {
                ProgressView().padding(60)
            }
        }
        .scrollDisabled(lock)
        .navigationTitle(monthTitle(month))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if !selection.on && !(items ?? []).isEmpty { ToolbarItem(placement: .topBarTrailing) { Button("Select") { selection.start() } } }
        }
        .choosing(selection) { await load() }
        .task { await load() }
        .refreshable { await load() }
        .fullScreenCover(item: $open, onDismiss: { Task { await load() } }) { v in Viewer(items: v.items, start: v.start) }
    }

    private func load() async {
        guard let api = library.api() else { return }
        do {
            items = try await api.month(month, of: "")
            problem = ""
        } catch {
            if items == nil { problem = error.localizedDescription }
        }
    }
}
