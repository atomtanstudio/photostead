// Browsing beyond the sync feed: places (and whether the server shows a map at all), AI
// characters, the prompt library, the timeline's months, what arrived lately, and whether search
// by meaning is running. A place's, a character's or a prompt's items come from the timeline
// endpoints (collection=place:{id}, character:{id}, prompt:{id}), as RemoteGrid reads them.

import Foundation
import Observation

/// A place items were taken, named offline by the server (the nearest town, from GeoNames).
struct PlaceRow: Decodable, Identifiable, Hashable {
    var id: String
    var name: String
    var region: String?
    var country: String?
    var countryCode: String?
    var lat: Double
    var lon: Double
    var count: Int
    var cover: String? // its newest item
    var coverThumbhash: Data?

    /// Under its name: the region when it says something more, and how many.
    var line: String {
        let n = count == 1 ? "1 item" : "\(count.formatted()) items"
        guard let region, !region.isEmpty, region != name else { return n }
        return "\(region) · \(n)"
    }
}

struct PlacesList: Decodable {
    struct Map: Decodable {
        var light: String
        var dark: String
    }
    var places: [PlaceRow]
    var map: Map? // none when the server's map is off (PHOTOSTEAD_MAP=off)
    var attribution: String
}

/// Places by country, as the web app lists them: countries in the server's order (it sends them
/// grouped, most items first within each), a place without one under Elsewhere, last.
func placesByCountry(_ places: [PlaceRow]) -> [(country: String, places: [PlaceRow])] {
    var order: [String] = []
    var groups: [String: [PlaceRow]] = [:]
    for p in places {
        let c = (p.country ?? "").isEmpty ? "" : p.country!
        if groups[c] == nil { order.append(c) }
        groups[c, default: []].append(p)
    }
    if let i = order.firstIndex(of: "") { order.append(order.remove(at: i)) }
    return order.map { (country: $0.isEmpty ? "Elsewhere" : $0, places: groups[$0] ?? []) }
}

/// An AI character: formed from generation metadata (their LoRAs), with a few recent items.
struct CharacterRow: Decodable, Identifiable, Hashable {
    struct Sample: Decodable, Hashable {
        var id: String
        var thumbhash: Data?
    }
    var id: String
    var name: String
    var count: Int
    var samples: [Sample]
    var firstDate: String?
    var lastDate: String?
}

/// A prompt from the prompt library: each distinct prompt once, with how often it was used.
struct PromptRow: Decodable, Identifiable, Hashable {
    var id: String
    var text: String
    var uses: Int
    var samples: [CharacterRow.Sample]
}

/// Whether search by meaning can run now, and whether it has ever looked at the library.
struct MeaningStatus: Decodable, Equatable {
    var available: Bool
    var embedded: Int
}

/// What a search found, and how.
struct SearchFound {
    var items: [Item]
    var byMeaning: Bool // false: by names only (search by meaning isn't running)
    var truncated: Bool
}

extension Server {
    func places() async throws -> PlacesList {
        try await call("GET", "api/v1/places", as: PlacesList.self)
    }

    func characters() async throws -> [CharacterRow] {
        struct Result: Decodable { var characters: [CharacterRow] }
        return try await call("GET", "api/v1/characters", as: Result.self).characters
    }

    /// The prompt library, most used first (or the prompts with these words in them).
    func prompts(matching q: String = "", limit: Int = 200) async throws -> (prompts: [PromptRow], total: Int) {
        struct Result: Decodable { var prompts: [PromptRow]; var total: Int }
        var path = "api/v1/prompts?sort=uses&limit=\(limit)"
        if !q.isEmpty { path += "&q=\(query(q))" }
        let r = try await call("GET", path, as: Result.self)
        return (r.prompts, r.total)
    }

    /// The timeline's months, newest first ("" is the whole timeline, as the server counts it).
    func months() async throws -> [Bucket] {
        try await buckets("")
    }

    /// Recently added: newest-arrived first, a page at a time (next is for the page after).
    func recent(after cursor: String? = nil, limit: Int = 300) async throws -> (items: [Item], next: String?) {
        struct Result: Decodable { var assets: [RemoteItem]; var next: String? }
        var path = "api/v1/timeline/recent?limit=\(limit)"
        if let cursor { path += "&cursor=\(query(cursor))" }
        let r = try await call("GET", path, as: Result.self)
        return (r.assets.map(\.item), r.next)
    }

    func meaning() async throws -> MeaningStatus {
        struct Status: Decodable { var search: MeaningStatus }
        return try await call("GET", "api/v1/status", as: Status.self).search
    }

    /// A search, saying whether it was by meaning (so a search by names only can say so).
    func find(_ q: String) async throws -> SearchFound {
        struct Result: Decodable { var mode: String; var assets: [RemoteItem]; var truncated: Bool? }
        let r = try await call("GET", "api/v1/search?q=\(query(q))", as: Result.self)
        return SearchFound(items: r.assets.map(\.item), byMeaning: r.mode != "names", truncated: r.truncated ?? false)
    }
}

/// What Collections and Search show beyond the people and counts Library keeps: places,
/// characters, prompts, the months, and search by meaning. From the server, kept for both tabs.
@MainActor
@Observable
final class Browse {
    static let shared = Browse()

    var places: [PlaceRow] = []
    var map = false // the server shows a map (its owner hasn't turned it off)
    var attribution = ""
    var characters: [CharacterRow] = [] // the ones with items
    var prompts = 0 // in the prompt library
    var months: [Bucket] = []
    var meaning: MeaningStatus?
    @ObservationIgnored private var last: Date?

    private init() {}

    /// From the server, unless it was a moment ago (both tabs ask as they open).
    func refresh(force: Bool = false) async {
        if !force, let last, Date().timeIntervalSince(last) < 30 { return }
        guard let api = Library.shared.api() else { return }
        last = Date()
        async let p = try? api.places()
        async let c = try? api.characters()
        async let m = try? api.months()
        async let s = try? api.meaning()
        async let pr = try? api.prompts(limit: 1)
        if let list = await p {
            places = list.places
            map = list.map != nil
            attribution = list.attribution
        }
        if let list = await c { characters = list.filter { $0.count > 0 } }
        if let list = await m { months = list.filter { $0.count > 0 } }
        if let status = await s { meaning = status }
        if let list = await pr { prompts = list.total }
    }
}

/// What search by meaning finds well, to try before typing anything.
let thingsToTry: [(q: String, title: String, symbol: String)] = [
    ("beach", "Beach", "beach.umbrella"), ("dog", "Dogs", "dog"), ("cat", "Cats", "cat"), ("food", "Food", "fork.knife"),
    ("snow", "Snow", "snowflake"), ("sunset", "Sunsets", "sun.horizon"), ("mountains", "Mountains", "mountain.2"),
    ("flowers", "Flowers", "camera.macro"),
]
