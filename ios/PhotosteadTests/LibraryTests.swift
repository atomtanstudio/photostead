@testable import Photostead
import XCTest

final class ThumbHashTests: XCTestCase {
    /// Real hashes from a library, decoded by the reference implementation (the web app's).
    func testMatchesTheReference() throws {
        let cases: [(String, Int, Int, [UInt8], [UInt8])] = [
            ("C/iFNIw38JZ4iIeHd/hCd0wPd4d4h4eICA==", 32, 26, [71, 14, 50, 23], [8, 71, 15, 129]),
            ("H/gJtYpwhH13d3d4d3d3d4+H93lm", 32, 23, [160, 127, 0, 255], [115, 122, 125, 255]),
            ("C/gJbZqAh4eIh3d3d4eIiIhPhMf1", 32, 23, [104, 0, 0, 255], [3, 131, 11, 255]),
        ]
        for (b64, w, h, first, mid) in cases {
            let (gw, gh, rgba) = try XCTUnwrap(ThumbHash.decode([UInt8](Data(base64Encoded: b64)!)))
            XCTAssertEqual(gw, w, b64)
            XCTAssertEqual(gh, h, b64)
            let m = ((gh >> 1) * gw + (gw >> 1)) * 4
            for (got, want) in zip(Array(rgba[0..<4]) + Array(rgba[m..<m + 4]), first + mid) {
                XCTAssertLessThanOrEqual(abs(Int(got) - Int(want)), 1, "\(b64): \(Array(rgba[0..<4])) \(Array(rgba[m..<m + 4]))")
            }
            XCTAssertNotNil(ThumbHash.image(Data(base64Encoded: b64)!))
        }
    }
}

final class LibraryStoreTests: XCTestCase {
    private func line(_ object: [String: Any]) -> Data { try! JSONSerialization.data(withJSONObject: object) }

    private func asset(_ n: Int, inLibrary: Bool = true, date: String = "2026-10-12T09:30:00.123456789-07:00") -> Data {
        line(["type": "asset", "asset": ["id": String(format: "00000000-0000-0000-0000-%012d", n), "type": n % 10 == 0 ? "video" : "image",
                                          "takenAt": date, "localDate": "2026-\(String(format: "%02d", 1 + n % 12))-12", "width": 4000, "height": 3000,
                                          "thumbhash": "H/gJtYpwhH13d3d4d3d3d4+H93lm", "render": "done", "ownerId": "o", "visibility": "timeline",
                                          "sha256": "x", "fileName": "IMG_\(n).HEIC", "inLibrary": inLibrary]])
    }

    func testApplyingTheFeed() async throws {
        let file = FileManager.default.temporaryDirectory.appending(path: "library-\(UUID()).sqlite")
        let store = try LibraryStore(file: file)
        var lines = (0..<5).map { asset($0) }
        lines.append(asset(5, inLibrary: false)) // only in an album shared with you
        lines.append(line(["type": "album", "album": ["id": "al", "title": "Trip", "ownerId": "o", "kind": "manual", "role": "owner",
                                                       "updatedAt": "2026-10-20T08:00:00Z", "assetIds": [String(format: "00000000-0000-0000-0000-%012d", 5)]]]))
        lines.append(line(["type": "checkpoint", "cursor": "1234"]))
        let applied = try await store.apply(lines)
        XCTAssertEqual(applied.cursor, "1234")
        XCTAssertEqual(applied.items, 6)
        let timeline = try await store.timeline()
        XCTAssertEqual(timeline.count, 5)
        XCTAssertEqual(timeline.first?.thumbhash?.isEmpty, false)
        let albums = try await store.albums()
        XCTAssertEqual(albums.first?.count, 1)
        XCTAssertEqual(albums.first?.cover?.fileName, "IMG_5.HEIC")
        let cursor = await store.meta("cursor")
        XCTAssertEqual(cursor, "1234")

        // Gone: out of the timeline and its albums. Reset: everything again.
        _ = try await store.apply([line(["type": "assetGone", "id": String(format: "00000000-0000-0000-0000-%012d", 5)]), line(["type": "checkpoint", "cursor": "1300"])])
        let afterGone = try await store.album("al")
        XCTAssertEqual(afterGone.count, 0)
        _ = try await store.apply([line(["type": "reset"]), asset(9), line(["type": "checkpoint", "cursor": "1400"])])
        let afterReset = try await store.timeline()
        XCTAssertEqual(afterReset.map(\.fileName), ["IMG_9.HEIC"])
    }

    /// Adult items, as the feed marks them: kept, and cleared when an item comes again without it.
    func testAdultFromTheFeed() async throws {
        let file = FileManager.default.temporaryDirectory.appending(path: "library-\(UUID()).sqlite")
        let store = try LibraryStore(file: file)
        var adult = try JSONSerialization.jsonObject(with: asset(1)) as! [String: Any]
        var a = adult["asset"] as! [String: Any]
        a["adult"] = true
        adult["asset"] = a
        _ = try await store.apply([line(adult), asset(2), line(["type": "checkpoint", "cursor": "10"])])
        let marked = try await store.timeline().filter(\.adult).map(\.fileName)
        XCTAssertEqual(marked, ["IMG_1.HEIC"])
        _ = try await store.apply([asset(1), line(["type": "checkpoint", "cursor": "11"])]) // its owner said it isn't
        let none = try await store.timeline().filter(\.adult)
        XCTAssertTrue(none.isEmpty)
    }

    /// Everything again (no cursor, or a reset): the library stays as it was until it has all come,
    /// then what didn't come is gone, albums too.
    func testReadingEverythingAgain() async throws {
        let file = FileManager.default.temporaryDirectory.appending(path: "library-\(UUID()).sqlite")
        let store = try LibraryStore(file: file)
        let id = { (n: Int) in String(format: "00000000-0000-0000-0000-%012d", n) }
        let album = { (aid: String, items: [Int]) in
            self.line(["type": "album", "album": ["id": aid, "title": aid, "ownerId": "o", "kind": "manual", "role": "owner",
                                                  "updatedAt": "2026-10-20T08:00:00Z", "assetIds": items.map(id)]])
        }
        _ = try await store.apply([asset(1), asset(2), asset(3), album("kept", [1, 3]), album("gone", [2]), line(["type": "checkpoint", "cursor": "5"])])

        try await store.readingEverything()
        _ = try await store.apply([asset(1), album("kept", [1, 3])]) // cut short here
        let during = try await store.timeline().count
        XCTAssertEqual(during, 3, "a read cut short emptied the library")

        try await store.readingEverything() // the next read, from the start
        _ = try await store.apply([asset(1), album("kept", [1, 3])])
        _ = try await store.apply([asset(3), line(["type": "checkpoint", "cursor": "9"])])
        let after = try await store.timeline().map(\.fileName).sorted()
        XCTAssertEqual(after, ["IMG_1.HEIC", "IMG_3.HEIC"])
        let albums = try await store.albums().map(\.id)
        XCTAssertEqual(albums, ["kept"])
        let kept = try await store.album("kept").count
        XCTAssertEqual(kept, 2)

        // A reset partway through a read since a cursor: the same.
        _ = try await store.apply([line(["type": "reset"]), asset(3)])
        let midReset = try await store.timeline().count
        XCTAssertEqual(midReset, 2)
        _ = try await store.apply([line(["type": "checkpoint", "cursor": "12"])])
        let afterReset = try await store.timeline().map(\.fileName)
        XCTAssertEqual(afterReset, ["IMG_3.HEIC"])
        let keptAfterReset = try await store.albums()
        XCTAssertTrue(keptAfterReset.isEmpty)
    }

    /// A library on the phone from before the feed said what's adult: read again from the start.
    func testAnOlderLibraryStartsOver() async throws {
        let file = FileManager.default.temporaryDirectory.appending(path: "library-\(UUID()).sqlite")
        do {
            let old = try SQLite(path: file.path)
            try old.exec("""
                CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT);
                CREATE TABLE assets (
                    id TEXT PRIMARY KEY, type TEXT, taken_at REAL, local_date TEXT, width INTEGER, height INTEGER, duration_ms INTEGER,
                    thumbhash BLOB, render TEXT, motion_id TEXT, motion_of TEXT, owner_id TEXT, visibility TEXT, ai TEXT, sha256 TEXT,
                    file_name TEXT, favorite INTEGER, rating INTEGER, shared INTEGER, in_library INTEGER, stack_size INTEGER, stacked INTEGER,
                    flag TEXT, label TEXT);
                INSERT INTO meta VALUES ('cursor', '99'), ('server', 's');
                """)
        }
        let store = try LibraryStore(file: file)
        let cursor = await store.meta("cursor"), server = await store.meta("server")
        XCTAssertNil(cursor)
        XCTAssertEqual(server, "s")
        _ = try await store.apply([asset(1), line(["type": "checkpoint", "cursor": "100"])])
        let again = try LibraryStore(file: file) // opened again: nothing more to add, the cursor stays
        let kept = await again.meta("cursor")
        XCTAssertEqual(kept, "100")
    }

    /// A library the size of a large one (100,000 items): applied and read back in good time.
    func testAWholeLibrary() async throws {
        let file = FileManager.default.temporaryDirectory.appending(path: "library-\(UUID()).sqlite")
        let store = try LibraryStore(file: file)
        let lines = (0..<100_000).map { asset($0) }
        let start = Date()
        for batch in stride(from: 0, to: lines.count, by: 2000) {
            _ = try await store.apply(Array(lines[batch..<min(batch + 2000, lines.count)]))
        }
        let applied = Date().timeIntervalSince(start)
        let readStart = Date()
        let timeline = try await store.timeline()
        let months = Library.group(timeline)
        let read = Date().timeIntervalSince(readStart)
        print("PHOTOSTEAD-SCALE applied 100000 in \(String(format: "%.1f", applied))s, read and grouped in \(String(format: "%.2f", read))s")
        XCTAssertEqual(timeline.count, 100_000)
        XCTAssertEqual(months.count, 12)
        XCTAssertLessThan(applied, 30)
        XCTAssertLessThan(read, 5)
    }
}

/// Which pictures are blurred: adult ones, unless you show them, until you tap Show.
@MainActor
final class AdultTests: XCTestCase {
    func testVeiled() {
        let library = Library.shared
        let was = library.adult
        defer { library.adult = was; library.revealed = [] }
        var item = Item(id: "a", type: "image", takenAt: .now, localDate: "2026-11-13", width: 1, height: 1, durationMs: 0, thumbhash: nil,
                        motionID: nil, favorite: false, fileName: "", ai: "")
        library.adult = "blur"
        XCTAssertFalse(library.veiled(item), "not adult")
        item.adult = true
        XCTAssertTrue(library.veiled(item))
        library.revealed.insert("a")
        XCTAssertFalse(library.veiled(item), "shown with Show")
        library.revealed = []
        library.adult = "hide" // an album's cover, say
        XCTAssertTrue(library.veiled(item))
        library.adult = "show"
        XCTAssertFalse(library.veiled(item))
    }
}
