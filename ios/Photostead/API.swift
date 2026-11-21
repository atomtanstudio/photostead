// Photostead's API, as the phone uses it beyond the sync feed: searching and asking, an item's
// details, favorites, the trash, the archive, stars and flags, the Household, albums, people (and
// naming them), memories, the vault, the counts, and a collection's items a month at a time (Made
// with AI, Captured by me, the labels, a person, the trash, the vault).

import Foundation
import UIKit

/// An item as the timeline API sends it.
struct RemoteItem: Decodable {
    var id: String
    var type: String
    var takenAt: String
    var localDate: String
    var width: Int?
    var height: Int?
    var durationMs: Int?
    var thumbhash: Data?
    var motionId: String?
    var favorite: Bool?
    var ai: String?
    var rating: Int?
    var flag: String?
    var label: String?
    var shared: Bool?
    var adult: Bool?

    var item: Item {
        Item(id: id, type: type, takenAt: parseDate(takenAt) ?? .distantPast, localDate: localDate, width: width ?? 0, height: height ?? 0,
             durationMs: durationMs ?? 0, thumbhash: thumbhash, motionID: motionId, favorite: favorite ?? false, fileName: "", ai: ai ?? "",
             rating: rating ?? 0, flag: flag ?? "", label: label ?? "", shared: shared ?? false, adult: adult ?? false)
    }
}

struct Detail: Decodable {
    struct Ref: Decodable, Hashable {
        var id: String
        var name: String
    }
    struct Camera: Decodable {
        var make: String?
        var model: String?
        var lens: String?
        var focalMm: Double?
        var fNumber: Double?
        var exposureS: Double?
        var iso: Int?
    }
    struct Location: Decodable {
        var lat: Double
        var lon: Double
    }
    struct Generation: Decodable {
        struct Lora: Decodable, Hashable {
            var name: String
            var weight: Double?
        }
        var kind: String
        var source: String
        var tool: String?
        var model: String?
        var loras: [Lora]?
        var prompt: String?
        var negative: String?
        var seed: String?
        var sampler: String?
        var steps: Int?
        var cfg: Double?
    }
    var id: String
    var fileName: String
    var edited: Bool? // it has an edit (made on the web); the original is as it was
    var folderPath: String?
    var library: String?
    var fileSize: Int64?
    var mime: String?
    var camera: Camera?
    var location: Location?
    var title: String?
    var description: String?
    var keywords: [String]?
    var albums: [Ref]?
    var people: [Ref]?
    var generation: Generation?
    var labels: [String]?
    var archived: Bool?
    var trashed: Bool?
}

struct Person: Decodable, Identifiable, Hashable {
    var id: String
    var name: String
    var count: Int
    var coverFace: String?
    var cover: String?
    var coverThumbhash: Data?
    var kind: String?
    var hidden: Bool?
}

struct Counts: Decodable {
    var all: Int
    var videos: Int
    var favorites: Int
    var archived: Int
    var albums: Int
    var people: Int
    var ai: Int
    var captured: Int
    var labels: [String: Int]
    var trash: Int
}

struct Answer: Decodable {
    var answer: String
    var describe: String
    var assets: [RemoteItem]
    var total: Int
    var truncated: Bool
    var notes: [String]?
}

/// You, and how you've set Photostead up.
struct Me: Decodable {
    struct Settings: Decodable {
        var organize: Bool // stars, flags and color labels
        var hideMemories: Bool
        var adult: String? // adult content: show (or nil), blur or hide
    }
    var id: String
    var displayName: String
    var settings: Settings
}

struct Household: Decodable {
    struct Member: Decodable { var userId: String? }
    var name: String
    var members: [Member]
}

/// A memory: a day in earlier years, or someone, with the items it shows.
struct Memory: Decodable, Identifiable {
    var key: String
    var kind: String
    var title: String
    var subtitle: String
    var count: Int
    var assets: [RemoteItem]
    var id: String { key }
}

struct VaultState: Decodable {
    var hasPin: Bool
    var unlocked: Bool
    var waitUntil: Date?
    var items: Int?
}

struct Bucket: Decodable, Hashable {
    var month: String
    var count: Int
}

/// The labels Photostead sorts things into, as the web app names them.
let utilityLabels: [(name: String, title: String, symbol: String)] = [
    ("screenshot", "Screenshots", "iphone"), ("document", "Documents", "doc.text"), ("receipt", "Receipts", "receipt"),
    ("id_card", "IDs", "person.text.rectangle"), ("qr_code", "QR codes", "qrcode"),
]

/// A value for a query string: everything but letters, digits and -._~ encoded.
func query(_ value: String) -> String {
    value.addingPercentEncoding(withAllowedCharacters: CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~")) ?? value
}

private struct IDs: Encodable {
    var assetIds: [String]
}

private struct Changed: Decodable {
    var changed: Int
}

extension Server {
    func search(_ q: String) async throws -> [Item] {
        struct Result: Decodable { var assets: [RemoteItem] }
        return try await call("GET", "api/v1/search?q=\(query(q))", as: Result.self).assets.map(\.item)
    }

    func ask(_ q: String) async throws -> Answer {
        struct Question: Encodable { var q: String }
        return try await call("POST", "api/v1/ask", body: Question(q: q), as: Answer.self)
    }

    func detail(_ id: String) async throws -> Detail {
        try await call("GET", "api/v1/assets/\(id)", as: Detail.self)
    }

    func favorite(_ ids: [String], _ on: Bool) async throws {
        struct Body: Encodable { var assetIds: [String]; var favorite: Bool }
        _ = try await call("POST", "api/v1/assets/favorite", body: Body(assetIds: ids, favorite: on), as: Changed.self)
    }

    func trash(_ ids: [String]) async throws {
        _ = try await call("POST", "api/v1/assets/trash", body: IDs(assetIds: ids), as: Changed.self)
    }

    func restore(_ ids: [String]) async throws {
        _ = try await call("POST", "api/v1/assets/restore", body: IDs(assetIds: ids), as: Changed.self)
    }

    func addToAlbum(_ album: String, _ ids: [String]) async throws {
        struct Album: Decodable { var id: String }
        _ = try await call("POST", "api/v1/albums/\(album)/add", body: IDs(assetIds: ids), as: Album.self)
    }

    func createAlbum(_ title: String, _ ids: [String]) async throws -> String {
        struct Body: Encodable { var title: String; var assetIds: [String] }
        struct Album: Decodable { var id: String }
        return try await call("POST", "api/v1/albums", body: Body(title: title, assetIds: ids), as: Album.self).id
    }

    func people() async throws -> [Person] {
        struct Result: Decodable { var people: [Person] }
        return try await call("GET", "api/v1/people", as: Result.self).people
    }

    func archive(_ ids: [String], _ on: Bool) async throws -> Int {
        struct Body: Encodable { var assetIds: [String]; var archived: Bool }
        return try await call("POST", "api/v1/assets/archive", body: Body(assetIds: ids, archived: on), as: Changed.self).changed
    }

    /// Into the Household space (for everyone in it), or out.
    func shareWithHousehold(_ ids: [String], _ on: Bool) async throws {
        struct Reply: Decodable {}
        _ = try await call("POST", on ? "api/v1/household/share" : "api/v1/household/unshare", body: IDs(assetIds: ids), as: Reply.self)
    }

    /// Stars (0 takes them off), a flag ("none" takes it off), a color label ("none" too).
    func mark(_ ids: [String], rating: Int? = nil, flag: String? = nil, label: String? = nil) async throws {
        struct Body: Encodable { var assetIds: [String]; var rating: Int?; var flag: String?; var label: String? }
        _ = try await call("POST", "api/v1/assets/marks", body: Body(assetIds: ids, rating: rating, flag: flag, label: label), as: Changed.self)
    }

    func me() async throws -> Me {
        try await call("GET", "api/v1/auth/me", as: Me.self)
    }

    /// Adult content shown, blurred or hidden: your setting, for the web too.
    func setAdult(_ mode: String) async throws -> Me {
        struct Body: Encodable { var adult: String }
        return try await call("PATCH", "api/v1/auth/me/settings", body: Body(adult: mode), as: Me.self)
    }

    func household() async throws -> Household {
        try await call("GET", "api/v1/household", as: Household.self)
    }

    func memories() async throws -> [Memory] {
        struct Result: Decodable { var memories: [Memory] }
        return try await call("GET", "api/v1/memories", as: Result.self).memories
    }

    /// Names someone (or names them again).
    func name(person id: String, _ name: String) async throws {
        struct Body: Encodable { var name: String }
        struct Reply: Decodable {}
        _ = try await call("PATCH", "api/v1/people/\(id)", body: Body(name: name), as: Reply.self)
    }

    /// The same person as someone named already: their faces and items go to them.
    func merge(person id: String, into: String) async throws {
        struct Body: Encodable { var into: String }
        struct Reply: Decodable {}
        _ = try await call("POST", "api/v1/people/\(id)/merge", body: Body(into: into), as: Reply.self)
    }

    // MARK: The vault

    func vault() async throws -> VaultState {
        try await call("GET", "api/v1/vault", as: VaultState.self)
    }

    func unlockVault(pin: String) async throws -> VaultState {
        struct Body: Encodable { var pin: String }
        return try await call("POST", "api/v1/vault/unlock", body: Body(pin: pin), as: VaultState.self)
    }

    func lockVault() async throws {
        _ = try await call("POST", "api/v1/vault/lock", as: VaultState.self)
    }

    func setVaultPIN(_ pin: String) async throws -> VaultState {
        struct Body: Encodable { var pin: String }
        return try await call("POST", "api/v1/vault/pin", body: Body(pin: pin), as: VaultState.self)
    }

    func toVault(_ ids: [String]) async throws {
        _ = try await call("POST", "api/v1/vault/add", body: IDs(assetIds: ids), as: Changed.self)
    }

    func outOfVault(_ ids: [String]) async throws {
        _ = try await call("POST", "api/v1/vault/remove", body: IDs(assetIds: ids), as: Changed.self)
    }

    func counts() async throws -> Counts {
        try await call("GET", "api/v1/library/counts", as: Counts.self)
    }

    /// A collection's months (newest first), and how many in each.
    func buckets(_ collection: String) async throws -> [Bucket] {
        struct Result: Decodable { var buckets: [Bucket] }
        return try await call("GET", "api/v1/timeline/buckets?collection=\(query(collection))", as: Result.self).buckets
    }

    /// A collection's items in one month.
    func month(_ month: String, of collection: String) async throws -> [Item] {
        struct Result: Decodable { var assets: [RemoteItem] }
        return try await call("GET", "api/v1/timeline?collection=\(query(collection))&month=\(query(month))", as: Result.self).assets.map(\.item)
    }

    /// An item's original, as a file on the phone named as it was (for the share sheet).
    func original(_ id: String, named name: String) async throws -> URL {
        try await file("api/v1/assets/\(id)/original", id: id, named: name)
    }

    /// An item at full size, as a file on the phone: the edited photo when it's been edited (what
    /// Photostead shows), otherwise the original.
    func fullSize(_ id: String, named name: String, edited: Bool) async throws -> URL {
        guard edited else { return try await original(id, named: name) }
        let base = (name as NSString).deletingPathExtension
        return try await file("api/v1/assets/\(id)/edited", id: id, named: (base.isEmpty ? id : base) + " (edited).jpg")
    }

    private func file(_ path: String, id: String, named name: String) async throws -> URL {
        let request = try await request("GET", path)
        let (file, response) = try await session.download(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else { throw PhotosteadError.refused(status, "") }
        let dir = FileManager.default.temporaryDirectory.appending(path: "share-\(UUID().uuidString)", directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let out = dir.appending(path: name.isEmpty ? id : name)
        try FileManager.default.moveItem(at: file, to: out)
        return out
    }
}

// MARK: Sharing

/// A share link: anyone with it sees the album, no account.
struct ShareLink: Decodable, Identifiable {
    var id: String
    var url: String
    var qr: String? // the address as a QR code (a PNG data URL)
    var `public`: Bool // works from the internet (Funnel on, or a public address)
    var hasPassword: Bool
    var expiresAt: Date?
    var allowDownload: Bool
    var allowUpload: Bool
    var views: Int

    var qrImage: UIImage? {
        guard let qr, let comma = qr.firstIndex(of: ","), let data = Data(base64Encoded: String(qr[qr.index(after: comma)...])) else { return nil }
        return UIImage(data: data)
    }
}

/// Someone an album is shared with (or could be): a household member, or a guest with a personal link.
struct AlbumPerson: Decodable, Identifiable {
    struct Link: Decodable {
        var url: String
        var `public`: Bool
        var devices: Int
        var maxDevices: Int
    }
    var id: String
    var name: String
    var guest: Bool?
    var role: String?
    var link: Link?
}

struct AlbumPeople: Decodable {
    var members: [AlbumPerson]
    var candidates: [AlbumPerson]
}

/// What the server says about an album that the sync feed doesn't: who shared it, and with how many.
struct AlbumInfo: Decodable {
    struct Owner: Decodable { var name: String }
    var role: String
    var owner: Owner? // whose it is, when it's shared with you
    var members: Int? // how many it's shared with (yours)

    /// Shared with you, or by you with someone: there's someone to talk to.
    var talks: Bool { role != "owner" || (members ?? 0) > 0 }
}

/// Something that happened in a shared album: someone added items, or said something.
struct AlbumActivity: Decodable, Identifiable {
    var kind: String // added, comment
    var commentID: String?
    var mine: Bool? // a comment you may delete: yours, or any in your album
    var who: String
    var at: Date
    var items: Int?
    var assets: [String]?
    var body: String?
    var assetId: String?

    var id: String { commentID ?? "\(kind)/\(who)/\(at.timeIntervalSince1970)" }

    enum CodingKeys: String, CodingKey {
        case kind, mine, who, at, items, assets, body, assetId
        case commentID = "id"
    }
}

struct AlbumComment: Decodable, Identifiable {
    var id: String
    var assetId: String?
    var author: String
    var guest: Bool?
    var mine: Bool // you may delete it: yours, or any in your album
    var body: String
    var createdAt: Date
}

/// An item someone picked: everyone who did, for the album's owner; only yours, for anyone else.
struct AlbumPick: Decodable, Identifiable {
    var assetId: String
    var people: [String]
    var mine: Bool

    var id: String { assetId }
}

extension Server {
    func albumLinks(_ album: String) async throws -> [ShareLink] {
        struct Result: Decodable { var links: [ShareLink] }
        return try await call("GET", "api/v1/albums/\(album)/links", as: Result.self).links
    }

    func newLink(_ album: String, downloads: Bool = true, uploads: Bool = false) async throws -> ShareLink {
        struct Body: Encodable { var allowDownload: Bool; var allowUpload: Bool }
        return try await call("POST", "api/v1/albums/\(album)/links", body: Body(allowDownload: downloads, allowUpload: uploads), as: ShareLink.self)
    }

    func editLink(_ link: ShareLink, downloads: Bool, uploads: Bool) async throws -> ShareLink {
        struct Body: Encodable { var allowDownload: Bool; var allowUpload: Bool; var expiresAt: Date? }
        return try await call("PATCH", "api/v1/links/\(link.id)", body: Body(allowDownload: downloads, allowUpload: uploads, expiresAt: link.expiresAt), as: ShareLink.self)
    }

    func deleteLink(_ id: String) async throws {
        _ = try await request("DELETE", "api/v1/links/\(id)").send(self)
    }

    func albumPeople(_ album: String) async throws -> AlbumPeople {
        try await call("GET", "api/v1/albums/\(album)/people", as: AlbumPeople.self)
    }

    /// Shares with someone by name, with no account to make: they get a personal link.
    func shareWithNew(_ album: String, name: String) async throws -> AlbumPeople {
        struct Body: Encodable { var name: String }
        return try await call("POST", "api/v1/albums/\(album)/people", body: Body(name: name), as: AlbumPeople.self)
    }

    func share(_ album: String, with user: String, role: String) async throws {
        struct Body: Encodable { var role: String }
        struct Reply: Decodable {}
        _ = try await call("PUT", "api/v1/albums/\(album)/people/\(user)", body: Body(role: role), as: Reply.self)
    }

    func unshare(_ album: String, with user: String) async throws {
        _ = try await request("DELETE", "api/v1/albums/\(album)/people/\(user)").send(self)
    }

    // A shared album's talk: its activity (who added what, and comments), an item's comments, and
    // picks (proofing: each person picks the ones they like, for the owner to see).

    func albumInfo(_ album: String) async throws -> AlbumInfo {
        try await call("GET", "api/v1/albums/\(album)", as: AlbumInfo.self)
    }

    func albumActivity(_ album: String) async throws -> [AlbumActivity] {
        struct Result: Decodable { var activity: [AlbumActivity] }
        return try await call("GET", "api/v1/albums/\(album)/activity", as: Result.self).activity
    }

    func albumComments(_ album: String, item: String) async throws -> [AlbumComment] {
        struct Result: Decodable { var comments: [AlbumComment] }
        return try await call("GET", "api/v1/albums/\(album)/comments?asset=\(query(item))", as: Result.self).comments
    }

    /// Says something about the album, or about one of its items.
    func comment(_ album: String, _ body: String, item: String? = nil) async throws {
        struct Body: Encodable { var body: String; var assetId: String? }
        _ = try await call("POST", "api/v1/albums/\(album)/comments", body: Body(body: body, assetId: item), as: AlbumComment.self)
    }

    func deleteComment(_ album: String, _ id: String) async throws {
        _ = try await request("DELETE", "api/v1/albums/\(album)/comments/\(id)").send(self)
    }

    func albumPicks(_ album: String) async throws -> [AlbumPick] {
        struct Result: Decodable { var picks: [AlbumPick] }
        return try await call("GET", "api/v1/albums/\(album)/picks", as: Result.self).picks
    }

    func pick(_ album: String, _ item: String, _ on: Bool) async throws {
        _ = try await request(on ? "PUT" : "DELETE", "api/v1/albums/\(album)/picks/\(item)").send(self)
    }

    /// A file from the phone into an album (yours, or one you contribute to): into your library or
    /// its owner's, and the album.
    func upload(_ file: URL, named name: String, toAlbum album: String) async throws {
        let boundary = "photostead-\(UUID().uuidString)"
        let body = FileManager.default.temporaryDirectory.appending(path: "upload-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: body) }
        FileManager.default.createFile(atPath: body.path, contents: nil)
        let out = try FileHandle(forWritingTo: body)
        let safe = name.replacingOccurrences(of: "\"", with: "")
        out.write(Data("--\(boundary)\r\nContent-Disposition: form-data; name=\"file\"; filename=\"\(safe)\"\r\nContent-Type: application/octet-stream\r\n\r\n".utf8))
        let input = try FileHandle(forReadingFrom: file)
        while let chunk = try input.read(upToCount: 4 << 20), !chunk.isEmpty { out.write(chunk) }
        try input.close()
        out.write(Data("\r\n--\(boundary)--\r\n".utf8))
        try out.close()
        var request = try await request("POST", "api/v1/albums/\(album)/upload", headers: ["Content-Type": "multipart/form-data; boundary=\(boundary)"])
        request.timeoutInterval = 600
        let (data, response) = try await session.upload(for: request, fromFile: body)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw PhotosteadError.refused(status, problemDetail(data)) }
    }
}

private extension URLRequest {
    /// Sends a request with no reply to read (a DELETE).
    func send(_ server: Server) async throws {
        let (data, response) = try await server.session.data(for: self)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw PhotosteadError.refused(status, problemDetail(data)) }
    }
}
