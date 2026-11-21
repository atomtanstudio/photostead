// The library on the phone: what Photostead's sync feed sent (every item and album you may see), in
// SQLite, kept in step with only what changed since (the feed's cursor). Browsing reads from here,
// so it's quick and works without the server; pictures come from the server as they're shown.

import Foundation
import SQLite3

/// An item as the library shows it.
struct Item: Identifiable, Hashable, Sendable {
    var id: String
    var type: String
    var takenAt: Date
    var localDate: String
    var width: Int
    var height: Int
    var durationMs: Int
    var thumbhash: Data?
    var motionID: String?
    var favorite: Bool
    var fileName: String
    var ai: String
    var owner = ""
    var rating = 0 // stars, yours (shown when Ratings & flags is on)
    var flag = "" // pick or reject
    var label = "" // a color
    var shared = false // in the Household space
    var archived = false
    var adult = false // nudity or sex, by a scan or its owner: blurred or hidden by your setting (Library.adult)

    var isVideo: Bool { type == "video" }
}

struct AlbumRow: Identifiable, Hashable, Sendable {
    var id: String
    var title: String
    var kind: String
    var role: String
    var cover: Item?
    var count: Int
    var updatedAt: Date
}

/// A line of the sync feed.
private struct FeedLine: Decodable {
    struct Asset: Decodable {
        var id: String
        var type: String
        var takenAt: String
        var localDate: String
        var width: Int?
        var height: Int?
        var durationMs: Int?
        var thumbhash: Data?
        var render: String
        var motionId: String?
        var motionOf: String?
        var ownerId: String
        var visibility: String
        var ai: String?
        var sha256: String?
        var fileName: String?
        var favorite: Bool?
        var rating: Int?
        var flag: String?
        var label: String?
        var shared: Bool?
        var inLibrary: Bool?
        var stackSize: Int?
        var stacked: Bool?
        var adult: Bool?
    }
    struct Album: Decodable {
        var id: String
        var title: String
        var description: String?
        var ownerId: String
        var parentId: String?
        var kind: String
        var coverId: String?
        var role: String
        var updatedAt: String
        var assetIds: [String]
    }
    var type: String
    var asset: Asset?
    var album: Album?
    var id: String?
    var cursor: String?
}

private let transient = unsafeBitCast(-1, to: sqlite3_destructor_type.self)

/// A small SQLite connection: statements, bound and stepped.
final class SQLite {
    private var db: OpaquePointer?

    init(path: String) throws {
        guard sqlite3_open_v2(path, &db, SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE | SQLITE_OPEN_NOMUTEX, nil) == SQLITE_OK else {
            throw StoreError(message: "Couldn't open the library on this phone.")
        }
    }

    deinit { sqlite3_close(db) }

    func exec(_ sql: String) throws {
        var error: UnsafeMutablePointer<CChar>?
        if sqlite3_exec(db, sql, nil, nil, &error) != SQLITE_OK {
            let message = error.map { String(cString: $0) } ?? "?"
            sqlite3_free(error)
            throw StoreError(message: message)
        }
    }

    func prepare(_ sql: String) throws -> Statement {
        var stmt: OpaquePointer?
        guard sqlite3_prepare_v2(db, sql, -1, &stmt, nil) == SQLITE_OK, let stmt else {
            throw StoreError(message: String(cString: sqlite3_errmsg(db)))
        }
        return Statement(stmt)
    }

    final class Statement {
        private let stmt: OpaquePointer
        init(_ stmt: OpaquePointer) { self.stmt = stmt }
        deinit { sqlite3_finalize(stmt) }

        @discardableResult
        func bind(_ values: Any?...) -> Statement {
            sqlite3_reset(stmt)
            sqlite3_clear_bindings(stmt)
            for (i, value) in values.enumerated() {
                let n = Int32(i + 1)
                switch value {
                case let v as String: sqlite3_bind_text(stmt, n, v, -1, transient)
                case let v as Int: sqlite3_bind_int64(stmt, n, Int64(v))
                case let v as Bool: sqlite3_bind_int64(stmt, n, v ? 1 : 0)
                case let v as Double: sqlite3_bind_double(stmt, n, v)
                case let v as Data: _ = v.withUnsafeBytes { sqlite3_bind_blob(stmt, n, $0.baseAddress, Int32(v.count), transient) }
                default: sqlite3_bind_null(stmt, n)
                }
            }
            return self
        }

        func run() throws {
            let rc = sqlite3_step(stmt)
            guard rc == SQLITE_DONE || rc == SQLITE_ROW else { throw StoreError(message: "step \(rc)") }
        }

        func next() -> Bool { sqlite3_step(stmt) == SQLITE_ROW }
        func text(_ i: Int32) -> String? { sqlite3_column_text(stmt, i).map { String(cString: $0) } }
        func int(_ i: Int32) -> Int { Int(sqlite3_column_int64(stmt, i)) }
        func double(_ i: Int32) -> Double { sqlite3_column_double(stmt, i) }
        func blob(_ i: Int32) -> Data? {
            guard let p = sqlite3_column_blob(stmt, i) else { return nil }
            return Data(bytes: p, count: Int(sqlite3_column_bytes(stmt, i)))
        }
    }
}

struct StoreError: LocalizedError {
    var message: String
    var errorDescription: String? { message }
}

private let fractional: ISO8601DateFormatter = {
    let f = ISO8601DateFormatter()
    f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return f
}()
private let whole = ISO8601DateFormatter()

/// A date as Go writes it (RFC 3339, fractional seconds or not).
func parseDate(_ s: String) -> Date? { fractional.date(from: s) ?? whole.date(from: s) }

actor LibraryStore {
    private let db: SQLite
    let file: URL

    init(file: URL? = nil) throws {
        self.file = file ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appending(path: "library.sqlite")
        db = try SQLite(path: self.file.path)
        try db.exec("""
            PRAGMA journal_mode = WAL;
            PRAGMA synchronous = NORMAL;
            CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
            CREATE TABLE IF NOT EXISTS assets (
                id TEXT PRIMARY KEY, type TEXT, taken_at REAL, local_date TEXT, width INTEGER, height INTEGER, duration_ms INTEGER,
                thumbhash BLOB, render TEXT, motion_id TEXT, motion_of TEXT, owner_id TEXT, visibility TEXT, ai TEXT, sha256 TEXT,
                file_name TEXT, favorite INTEGER, rating INTEGER, shared INTEGER, in_library INTEGER, stack_size INTEGER, stacked INTEGER,
                flag TEXT, label TEXT, adult INTEGER);
            CREATE INDEX IF NOT EXISTS assets_timeline ON assets (in_library, visibility, local_date DESC, taken_at DESC);
            CREATE TABLE IF NOT EXISTS albums (id TEXT PRIMARY KEY, title TEXT, description TEXT, owner_id TEXT, parent_id TEXT, kind TEXT,
                cover_id TEXT, role TEXT, updated_at REAL);
            CREATE TABLE IF NOT EXISTS album_items (album_id TEXT, asset_id TEXT, position INTEGER, PRIMARY KEY (album_id, asset_id));
            CREATE INDEX IF NOT EXISTS album_items_asset ON album_items (asset_id);
            """)
        // A copy of what's on the server, read again at any time: kept out of the phone's own
        // backups (Apple's storage guidelines), with its write-ahead files.
        for suffix in ["", "-wal", "-shm"] {
            var url = URL(fileURLWithPath: self.file.path + suffix)
            var values = URLResourceValues()
            values.isExcludedFromBackup = true
            try? url.setResourceValues(values)
        }
        if (try? db.exec("ALTER TABLE assets ADD COLUMN flag TEXT")) != nil { // a library from before flags and labels: all of it again
            try db.exec("ALTER TABLE assets ADD COLUMN label TEXT")
            try db.exec("DELETE FROM meta WHERE key = 'cursor'")
        }
        if (try? db.exec("ALTER TABLE assets ADD COLUMN adult INTEGER")) != nil { // from before the feed said what's adult: all of it again
            try db.exec("DELETE FROM meta WHERE key = 'cursor'")
        }
    }

    func meta(_ key: String) -> String? {
        guard let s = try? db.prepare("SELECT value FROM meta WHERE key = ?") else { return nil }
        s.bind(key)
        return s.next() ? s.text(0) : nil
    }

    func setMeta(_ key: String, _ value: String?) throws {
        try db.prepare("INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value").bind(key, value).run()
    }

    /// Empties the library on the phone (and, with everything, which server it was and its cursor).
    func erase(everything: Bool = true) throws {
        try db.exec("DELETE FROM assets; DELETE FROM albums; DELETE FROM album_items;" + (everything ? " DELETE FROM meta;" : ""))
    }

    /// All of it is coming (a first read, or one from the start): what doesn't come again is gone,
    /// but only once it has all come, so a read cut short leaves the library as it was.
    private var rereading = false

    func readingEverything() throws {
        try db.exec("CREATE TEMP TABLE IF NOT EXISTS came (kind TEXT, id TEXT, PRIMARY KEY (kind, id)); DELETE FROM came;")
        rereading = true
    }

    // MARK: Applying the feed

    /// Applies feed lines (JSON, one per element), in one transaction. Returns the checkpoint's
    /// cursor if one came, and how many items were among them.
    func apply(_ lines: [Data]) throws -> (cursor: String?, items: Int) {
        let decoder = JSONDecoder()
        try db.exec("BEGIN")
        let upsert = try db.prepare("""
            INSERT INTO assets (id, type, taken_at, local_date, width, height, duration_ms, thumbhash, render, motion_id, motion_of, owner_id,
                visibility, ai, sha256, file_name, favorite, rating, shared, in_library, stack_size, stacked, flag, label, adult)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT (id) DO UPDATE SET type = excluded.type, taken_at = excluded.taken_at, local_date = excluded.local_date,
                width = excluded.width, height = excluded.height, duration_ms = excluded.duration_ms, thumbhash = excluded.thumbhash,
                render = excluded.render, motion_id = excluded.motion_id, motion_of = excluded.motion_of, owner_id = excluded.owner_id,
                visibility = excluded.visibility, ai = excluded.ai, sha256 = excluded.sha256, file_name = excluded.file_name,
                favorite = excluded.favorite, rating = excluded.rating, shared = excluded.shared, in_library = excluded.in_library,
                stack_size = excluded.stack_size, stacked = excluded.stacked, flag = excluded.flag, label = excluded.label,
                adult = excluded.adult
            """)
        let goneAsset = try db.prepare("DELETE FROM assets WHERE id = ?")
        let goneMembership = try db.prepare("DELETE FROM album_items WHERE asset_id = ?")
        let album = try db.prepare("""
            INSERT INTO albums (id, title, description, owner_id, parent_id, kind, cover_id, role, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT (id) DO UPDATE SET title = excluded.title, description = excluded.description, owner_id = excluded.owner_id,
                parent_id = excluded.parent_id, kind = excluded.kind, cover_id = excluded.cover_id, role = excluded.role, updated_at = excluded.updated_at
            """)
        let clearItems = try db.prepare("DELETE FROM album_items WHERE album_id = ?")
        let addItem = try db.prepare("INSERT OR IGNORE INTO album_items (album_id, asset_id, position) VALUES (?, ?, ?)")
        let goneAlbum = try db.prepare("DELETE FROM albums WHERE id = ?")
        if !rereading { try db.exec("CREATE TEMP TABLE IF NOT EXISTS came (kind TEXT, id TEXT, PRIMARY KEY (kind, id))") }
        let came = try db.prepare("INSERT OR IGNORE INTO came (kind, id) VALUES (?, ?)")
        var cursor: String?
        var items = 0
        do {
            for raw in lines {
                let line = try decoder.decode(FeedLine.self, from: raw)
                switch line.type {
                case "reset": // everything again: forgotten once it has all come
                    try db.exec("DELETE FROM came")
                    rereading = true
                case "asset":
                    guard let a = line.asset else { continue }
                    items += 1
                    if rereading { try came.bind("asset", a.id).run() }
                    try upsert.bind(a.id, a.type, (parseDate(a.takenAt) ?? .distantPast).timeIntervalSince1970, a.localDate, a.width ?? 0, a.height ?? 0,
                                    a.durationMs ?? 0, a.thumbhash, a.render, a.motionId, a.motionOf, a.ownerId, a.visibility, a.ai ?? "", a.sha256 ?? "",
                                    a.fileName ?? "", a.favorite ?? false, a.rating ?? 0, a.shared ?? false, a.inLibrary ?? false, a.stackSize ?? 0,
                                    a.stacked ?? false, a.flag ?? "", a.label ?? "", a.adult ?? false).run()
                case "assetGone":
                    guard let id = line.id else { continue }
                    try goneAsset.bind(id).run()
                    try goneMembership.bind(id).run()
                case "album":
                    guard let al = line.album else { continue }
                    if rereading { try came.bind("album", al.id).run() }
                    try album.bind(al.id, al.title, al.description ?? "", al.ownerId, al.parentId, al.kind, al.coverId, al.role,
                                   (parseDate(al.updatedAt) ?? .distantPast).timeIntervalSince1970).run()
                    try clearItems.bind(al.id).run()
                    for (i, asset) in al.assetIds.enumerated() { try addItem.bind(al.id, asset, i).run() }
                case "albumGone":
                    guard let id = line.id else { continue }
                    try goneAlbum.bind(id).run()
                    try clearItems.bind(id).run()
                case "checkpoint":
                    cursor = line.cursor
                    if rereading { // all of it came: what didn't is gone
                        try db.exec("""
                            DELETE FROM assets WHERE id NOT IN (SELECT id FROM came WHERE kind = 'asset');
                            DELETE FROM albums WHERE id NOT IN (SELECT id FROM came WHERE kind = 'album');
                            DELETE FROM album_items WHERE album_id NOT IN (SELECT id FROM albums) OR asset_id NOT IN (SELECT id FROM assets);
                            DELETE FROM came;
                            """)
                    }
                default:
                    break
                }
            }
            if let cursor { try setMeta("cursor", cursor) }
            try db.exec("COMMIT")
            if cursor != nil { rereading = false }
        } catch {
            try? db.exec("ROLLBACK")
            throw error
        }
        return (cursor, items)
    }

    // MARK: Reading

    private let itemColumns = """
        a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.motion_id, a.favorite, a.file_name, a.ai, \
        a.owner_id, a.rating, a.flag, a.label, a.shared, a.visibility, a.adult
        """
    private let itemCount: Int32 = 19

    private func item(_ s: SQLite.Statement) -> Item {
        Item(id: s.text(0) ?? "", type: s.text(1) ?? "image", takenAt: Date(timeIntervalSince1970: s.double(2)), localDate: s.text(3) ?? "",
             width: s.int(4), height: s.int(5), durationMs: s.int(6), thumbhash: s.blob(7), motionID: s.text(8), favorite: s.int(9) != 0,
             fileName: s.text(10) ?? "", ai: s.text(11) ?? "", owner: s.text(12) ?? "", rating: s.int(13), flag: s.text(14) ?? "",
             label: s.text(15) ?? "", shared: s.int(16) != 0, archived: s.text(17) == "archived", adult: s.int(18) != 0)
    }

    /// The timeline: your library (yours and the Household's), newest first by the day where it
    /// was taken (as the web app's), without what's shown under something else (a Live Photo's
    /// clip, an item under a stack's cover).
    func timeline() throws -> [Item] {
        let s = try db.prepare("""
            SELECT \(itemColumns) FROM assets a
            WHERE a.in_library = 1 AND a.visibility = 'timeline' AND a.motion_of IS NULL AND a.stacked = 0
            ORDER BY a.local_date DESC, a.taken_at DESC, a.id DESC
            """)
        var out: [Item] = []
        while s.next() { out.append(item(s)) }
        return out
    }

    /// A list on the phone: favorites, videos, or the archive (yours and the Household's).
    func list(_ which: String) throws -> [Item] {
        let filter = switch which {
        case "favorites": "a.favorite = 1 AND a.visibility IN ('timeline', 'archived')"
        case "videos": "a.type = 'video' AND a.in_library = 1 AND a.visibility = 'timeline'"
        default: "a.visibility = 'archived' AND a.in_library = 1"
        }
        let s = try db.prepare("""
            SELECT \(itemColumns) FROM assets a WHERE \(filter) AND a.motion_of IS NULL AND a.stacked = 0
            ORDER BY a.local_date DESC, a.taken_at DESC, a.id DESC
            """)
        var out: [Item] = []
        while s.next() { out.append(item(s)) }
        return out
    }

    /// Changed here before the feed says so (it will).
    func setFavorite(_ ids: [String], _ on: Bool) throws {
        let s = try db.prepare("UPDATE assets SET favorite = ? WHERE id = ?")
        for id in ids { try s.bind(on, id).run() }
    }

    func setArchived(_ ids: [String], _ on: Bool) throws {
        let s = try db.prepare("UPDATE assets SET visibility = ? WHERE (id = ? OR motion_of = ?) AND visibility IN ('timeline', 'archived')")
        for id in ids { try s.bind(on ? "archived" : "timeline", id, id).run() }
    }

    func setShared(_ ids: [String], _ on: Bool) throws {
        let s = try db.prepare("UPDATE assets SET shared = ? WHERE id = ? OR motion_of = ?")
        for id in ids { try s.bind(on, id, id).run() }
    }

    /// Stars, a flag, a label: the ones given (nil: as they were; 0 or "": off).
    func setMarks(_ ids: [String], rating: Int?, flag: String?, label: String?) throws {
        let s = try db.prepare("UPDATE assets SET rating = coalesce(?, rating), flag = coalesce(?, flag), label = coalesce(?, label) WHERE id = ?")
        for id in ids { try s.bind(rating, flag, label, id).run() }
    }

    func remove(_ ids: [String]) throws {
        let a = try db.prepare("DELETE FROM assets WHERE id = ?")
        let m = try db.prepare("DELETE FROM album_items WHERE asset_id = ?")
        for id in ids {
            try a.bind(id).run()
            try m.bind(id).run()
        }
    }

    func albums() throws -> [AlbumRow] {
        let s = try db.prepare("""
            SELECT \(itemColumns), al.id, al.title, al.kind, al.role, (SELECT count(*) FROM album_items ai WHERE ai.album_id = al.id), al.updated_at
            FROM albums al
            LEFT JOIN assets a ON a.id = coalesce(al.cover_id, (SELECT ai.asset_id FROM album_items ai WHERE ai.album_id = al.id ORDER BY ai.position LIMIT 1))
            ORDER BY al.updated_at DESC
            """)
        var out: [AlbumRow] = []
        while s.next() {
            let n = itemCount
            out.append(AlbumRow(id: s.text(n) ?? "", title: s.text(n + 1) ?? "", kind: s.text(n + 2) ?? "manual", role: s.text(n + 3) ?? "owner",
                                cover: s.text(0) == nil ? nil : item(s), count: s.int(n + 4), updatedAt: Date(timeIntervalSince1970: s.double(n + 5))))
        }
        return out
    }

    func album(_ id: String) throws -> [Item] {
        let s = try db.prepare("""
            SELECT \(itemColumns) FROM album_items ai JOIN assets a ON a.id = ai.asset_id
            WHERE ai.album_id = ? AND a.motion_of IS NULL ORDER BY ai.position
            """)
        s.bind(id)
        var out: [Item] = []
        while s.next() { out.append(item(s)) }
        return out
    }
}
