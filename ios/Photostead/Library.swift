// Browsing your Photostead library: kept on the phone from the sync feed (everything at first,
// then only what changed, from the cursor its last read ended with), and read from there.

import Foundation
import Observation

/// A month of the timeline.
struct Month: Identifiable, Hashable {
    var id: String // YYYY-MM
    var title: String
    var items: [Item]
    var start = 0 // where its first item is in the whole list (to choose a run across months)
}

/// What the Library shows: everything, or only photos, videos or favorites; everyone's, or only
/// yours, or only what's in the Household space.
enum Show: String, CaseIterable {
    case all, photos, videos, favorites
}

enum Whose: String, CaseIterable {
    case everyone, mine, household
}

@MainActor
@Observable
final class Library {
    static let shared = Library()

    var months: [Month] = []
    var timeline: [Item] = [] // newest first, as months has them (as shown: see show and whose)
    var total = 0 // in the library, whatever's shown
    var albums: [AlbumRow] = []
    var people: [Person] = []
    var unnamed: [Person] = [] // found by face recognition, not named yet
    var counts: Counts?
    var me: Me?
    var inHousehold = false // with someone else: Mine or the Household's
    var memories: [Memory] = []
    var show = Show(rawValue: UserDefaults.standard.string(forKey: "show") ?? "") ?? .all {
        didSet { UserDefaults.standard.set(show.rawValue, forKey: "show"); Task { await reload() } }
    }
    var whose = Whose(rawValue: UserDefaults.standard.string(forKey: "whose") ?? "") ?? .everyone {
        didSet { UserDefaults.standard.set(whose.rawValue, forKey: "whose"); Task { await reload() } }
    }
    /// Stars, flags and color labels: shown and used when Ratings & flags is on (on the web).
    var organize: Bool { me?.settings.organize ?? false }
    /// Adult content (nudity or sex), as you've set it, here or on the web: shown, blurred until you
    /// tap Show, or hidden. Kept for the next launch, so nothing shows before the server says.
    var adult = UserDefaults.standard.string(forKey: "adult") ?? "show" {
        didSet {
            guard adult != oldValue else { return }
            UserDefaults.standard.set(adult, forKey: "adult")
            if adult == "hide" || oldValue == "hide" { Task { await reload() } }
        }
    }
    /// Adult items shown with Show, until the app is closed.
    var revealed: Set<String> = []
    var syncing = false
    var received = 0
    var message = ""
    var lastSync: Date?
    /// Where pictures come from: the address that answered, and the device's token.
    private(set) var base: URL?
    private(set) var token: String?

    private var store: LibraryStore?
    private var server: Server?
    private var serverID = ""
    /// Asked to sync while a read was under way: one more read when it ends, and who's waiting for it.
    private var again = false
    private var waiting: [CheckedContinuation<Void, Never>] = []

    private init() {
        #if DEBUG
        if let at = ProcessInfo.processInfo.arguments.firstIndex(of: "-demoLibrary"), let n = Int(ProcessInfo.processInfo.arguments[at + 1]) {
            demo(n) // UI tests: a library this size, made up, with no server
            return
        }
        #endif
        store = try? LibraryStore()
        base = Pairing.load()?.candidates.first // the one that answered last: pictures can start at once
        token = Keychain.token
        Task { await reload() }
    }

    /// Reads the library on the phone into what the screens show.
    func reload() async {
        guard let store else { return }
        let all = (try? await store.timeline()) ?? []
        let rows = (try? await store.albums()) ?? []
        let (show, whose, me, hide) = (show, whose, me?.id ?? "", adult == "hide")
        let (items, grouped) = await Task.detached {
            let items = all.filter { item in
                if hide && item.adult { return false }
                let kind = switch show {
                case .all: true
                case .photos: !item.isVideo
                case .videos: item.isVideo
                case .favorites: item.favorite
                }
                let owner = switch whose {
                case .everyone: true
                case .mine: me.isEmpty || item.owner == me
                case .household: item.shared
                }
                return kind && owner
            }
            return (items, Library.group(items))
        }.value
        total = hide ? all.count(where: { !$0.adult }) : all.count
        timeline = items
        months = grouped
        albums = rows
    }

    nonisolated static func group(_ items: [Item]) -> [Month] {
        let parse = DateFormatter()
        parse.dateFormat = "yyyy-MM"
        parse.locale = Locale(identifier: "en_US_POSIX")
        let show = DateFormatter()
        show.setLocalizedDateFormatFromTemplate("MMMM yyyy")
        var out: [Month] = []
        for item in items {
            let key = String(item.localDate.prefix(7))
            if out.last?.id == key {
                out[out.count - 1].items.append(item)
            } else {
                let start = out.last.map { $0.start + $0.items.count } ?? 0
                out.append(Month(id: key, title: parse.date(from: key).map(show.string(from:)) ?? key, items: [item], start: start))
            }
        }
        return out
    }

    func items(inAlbum id: String) async -> [Item] {
        shown((try? await store?.album(id)) ?? [])
    }

    func list(_ which: String) async -> [Item] {
        shown((try? await store?.list(which)) ?? [])
    }

    /// Without adult items, if you hide them.
    private func shown(_ items: [Item]) -> [Item] {
        adult == "hide" ? items.filter { !$0.adult } : items
    }

    /// Whether this item's picture is blurred: adult, blurred (or hidden: an album's cover), and not
    /// shown with Show yet.
    func veiled(_ item: Item) -> Bool {
        item.adult && adult != "show" && !revealed.contains(item.id)
    }

    /// Adult content shown, blurred or hidden, from now on (on the web too).
    func setAdult(_ mode: String) async throws {
        guard let api = api() else { throw PhotosteadError.notPaired }
        let m = try await api.setAdult(mode)
        me = m
        adult = m.settings.adult ?? "show"
    }

    /// The API, at whichever address answers (made once, after pairing).
    func api() -> Server? {
        if let server { return server }
        guard let pairing = Pairing.load(), let token = Keychain.token else { return nil }
        server = Server(pairing: pairing, token: token)
        serverID = pairing.serverID
        return server
    }

    /// People and the counts, for Collections and Search: from the server, not the feed.
    func refreshExtras() async {
        guard let api = api() else { return }
        if let c = try? await api.counts() { counts = c }
        if let p = try? await api.people() {
            let shown = p.filter { $0.hidden != true }.sorted { $0.count > $1.count }
            people = shown.filter { !$0.name.isEmpty }
            unnamed = shown.filter { $0.name.isEmpty }
        }
        if let m = try? await api.me() {
            let was = me?.id
            me = m
            adult = m.settings.adult ?? "show"
            if was != m.id, whose == .mine { await reload() }
        }
        if let h = try? await api.household() { inHousehold = h.members.count > 1 }
        memories = me?.settings.hideMemories == true ? [] : ((try? await api.memories()) ?? [])
    }

    // MARK: Changing things (on the server, and here at once)

    func setFavorite(_ ids: [String], _ on: Bool) async throws {
        guard let api = api() else { throw PhotosteadError.notPaired }
        try await api.favorite(ids, on)
        try? await store?.setFavorite(ids, on)
        await reload()
    }

    func trash(_ ids: [String]) async throws {
        guard let api = api() else { throw PhotosteadError.notPaired }
        try await api.trash(ids)
        try? await store?.remove(ids)
        await reload()
        Task { await refreshExtras() }
    }

    func restore(_ ids: [String]) async throws {
        guard let api = api() else { throw PhotosteadError.notPaired }
        try await api.restore(ids)
        await sync() // back in the feed
        await refreshExtras()
    }

    /// Into the archive (out of the timeline), or back: your own.
    @discardableResult
    func archive(_ ids: [String], _ on: Bool) async throws -> Int {
        guard let api = api() else { throw PhotosteadError.notPaired }
        let n = try await api.archive(ids, on)
        try? await store?.setArchived(ids, on)
        await reload()
        Task { await refreshExtras() }
        return n
    }

    func shareWithHousehold(_ ids: [String], _ on: Bool) async throws {
        guard let api = api() else { throw PhotosteadError.notPaired }
        try await api.shareWithHousehold(ids, on)
        try? await store?.setShared(ids, on)
        await reload()
    }

    func mark(_ ids: [String], rating: Int? = nil, flag: String? = nil, label: String? = nil) async throws {
        guard let api = api() else { throw PhotosteadError.notPaired }
        try await api.mark(ids, rating: rating, flag: flag, label: label)
        try? await store?.setMarks(ids, rating: rating, flag: flag.map { $0 == "none" ? "" : $0 }, label: label.map { $0 == "none" ? "" : $0 })
        await reload()
    }

    /// Into the vault: out of everything else until it comes out again.
    func toVault(_ ids: [String]) async throws {
        guard let api = api() else { throw PhotosteadError.notPaired }
        try await api.toVault(ids)
        try? await store?.remove(ids)
        await reload()
        Task { await refreshExtras() }
    }

    func outOfVault(_ ids: [String]) async throws {
        guard let api = api() else { throw PhotosteadError.notPaired }
        try await api.outOfVault(ids)
        await sync() // back in the feed
    }

    func addToAlbum(_ album: String, _ ids: [String]) async throws {
        guard let api = api() else { throw PhotosteadError.notPaired }
        try await api.addToAlbum(album, ids)
        await sync()
    }

    @discardableResult
    func createAlbum(_ title: String, with ids: [String] = []) async throws -> String {
        guard let api = api() else { throw PhotosteadError.notPaired }
        let id = try await api.createAlbum(title, ids)
        await sync()
        return id
    }

    /// Catches up with Photostead: everything the first time (or after the server says start
    /// over), then what changed. Asked again while a read is under way (an album just made, say),
    /// it waits for one more read, which has the change.
    func sync() async {
        if syncing {
            again = true
            await withCheckedContinuation { waiting.append($0) }
            return
        }
        syncing = true
        received = 0
        message = ""
        repeat {
            again = false
            await read()
        } while again
        syncing = false
        let done = waiting
        waiting = []
        done.forEach { $0.resume() }
    }

    private func read() async {
        guard let pairing = Pairing.load(), let token = Keychain.token, let store else { return }
        if serverID != pairing.serverID || server == nil {
            if let had = await store.meta("server"), had != pairing.serverID { try? await store.erase() } // another server
            try? await store.setMeta("server", pairing.serverID)
            server = Server(pairing: pairing, token: token)
            serverID = pairing.serverID
        }
        guard let server else { return }
        do {
            base = try await server.base()
            self.token = token
            let since = await store.meta("cursor")
            if since == nil { try await store.readingEverything() } // all of it is coming: what isn't, is gone (once it has)
            var request = try await server.request("GET", "api/v1/sync/stream" + (since.map { "?since=\(query($0))" } ?? ""))
            request.timeoutInterval = 120
            let (bytes, response) = try await server.session.bytes(for: request)
            let status = (response as? HTTPURLResponse)?.statusCode ?? 0
            guard status == 200 else { throw PhotosteadError.refused(status, status == 401 ? "This phone was signed out of Photostead." : "") }
            var batch: [Data] = []
            var finished = false
            for try await line in bytes.lines {
                batch.append(Data(line.utf8))
                if line.contains("\"type\":\"checkpoint\"") { finished = true }
                if batch.count >= 2000 || finished {
                    let applied = try await store.apply(batch)
                    received += applied.items
                    batch.removeAll(keepingCapacity: true)
                }
            }
            if !batch.isEmpty { received += try await store.apply(batch).items }
            if !finished { message = "The library didn’t all arrive; it carries on next time." }
            lastSync = Date()
        } catch {
            message = error.localizedDescription
        }
        await reload()
    }

    #if DEBUG
    /// A made-up library of n items over the last 17 years (placeholders only), to try scrolling,
    /// the scrubber and the viewer at a real library's size.
    private func demo(_ n: Int) {
        let file = FileManager.default.temporaryDirectory.appending(path: "demo-library.sqlite")
        for suffix in ["", "-wal", "-shm"] { try? FileManager.default.removeItem(at: URL(fileURLWithPath: file.path + suffix)) }
        store = try? LibraryStore(file: file)
        let hashes = ["C/iFNIw38JZ4iIeHd/hCd0wPd4d4h4eICA==", "H/gJtYpwhH13d3d4d3d3d4+H93lm", "C/gJbZqAh4eIh3d3d4eIiIhPhMf1"]
        let now = Date().timeIntervalSince1970, span = 17 * 365 * 86400.0
        let day = DateFormatter()
        day.dateFormat = "yyyy-MM-dd"
        let iso = ISO8601DateFormatter()
        Task {
            var batch: [Data] = []
            for k in 0..<n {
                let at = Date(timeIntervalSince1970: now - span * Double(k) / Double(n))
                let line: [String: Any] = ["type": "asset", "asset": [
                    "id": String(format: "00000000-0000-4000-8000-%012d", k), "type": k % 15 == 0 ? "video" : "image",
                    "takenAt": iso.string(from: at), "localDate": day.string(from: at), "width": 4000, "height": 3000, "durationMs": 12000,
                    "thumbhash": hashes[k % 3], "render": "done", "ownerId": "demo", "visibility": "timeline", "fileName": "IMG_\(k).HEIC",
                    "inLibrary": true, "favorite": k % 40 == 0]]
                batch.append(try! JSONSerialization.data(withJSONObject: line))
                if batch.count == 2000 || k == n - 1 {
                    _ = try? await store?.apply(batch)
                    batch.removeAll()
                }
            }
            await reload()
        }
    }
    #endif

    /// Forgets the library on the phone (unpaired).
    func forget() async {
        try? await store?.erase()
        server = nil
        serverID = ""
        base = nil
        token = nil
        adult = "show" // the next server says
        revealed = []
        await reload()
    }

    func url(_ id: String, _ what: String) -> URL? {
        base?.appending(path: "api/v1/assets/\(id)/\(what)")
    }
}
