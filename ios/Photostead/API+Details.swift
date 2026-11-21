// An item's details as the viewer shows them: everything GET /api/v1/assets/{id} says, read the way
// the web app's details panel reads it (the place's name, people and pets, AI characters, the text in
// the picture, and how an AI picture was made). `Detail` (API.swift) stays as the lighter read that
// sharing and saving use.

import Foundation

struct ItemDetails: Decodable {
    /// Something the item belongs to: an album, a person or pet, an AI character.
    struct Ref: Decodable, Hashable, Identifiable {
        var id: String
        var name: String
        var source: String? // for people: faces (recognized) or immich (a suggestion)
        var kind: String? // for people: human or pet
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
        var place: String? // the nearest town, its region and country
        var placeId: String?
    }
    struct Owner: Decodable {
        var id: String
        var name: String
    }
    struct Generation: Decodable {
        struct Lora: Decodable, Hashable {
            var id: String?
            var name: String
            var weight: Double?
        }
        var kind: String // generated or edited
        var source: String
        var evidence: String?
        var tool: String?
        var model: String?
        var modelId: String?
        var loras: [Lora]?
        var prompt: String?
        var promptId: String?
        var promptUses: Int?
        var negative: String?
        var seed: String?
        var sampler: String?
        var scheduler: String?
        var steps: Int?
        var cfg: Double?
        var incomplete: Bool?
    }

    var id: String
    var type: String
    var takenAt: Date
    var offsetMin: Int? // the time zone it was taken in, when the file says
    var dateSource: String?
    var width: Int?
    var height: Int?
    var durationMs: Int?
    var mime: String?
    var fileName: String
    var folderPath: String?
    var library: String?
    var fileSize: Int64?
    var owner: Owner? // another member's, shared with the Household
    var shared: Bool?
    var camera: Camera?
    var location: Location?
    var title: String?
    var description: String?
    var keywords: [String]?
    var albums: [Ref]?
    var people: [Ref]?
    var characters: [Ref]?
    var generation: Generation?
    var text: String? // read from the picture
    var edited: Bool?
    var archived: Bool?
    var trashed: Bool?
}

extension Server {
    func details(_ id: String) async throws -> ItemDetails {
        try await call("GET", "api/v1/assets/\(id)", as: ItemDetails.self)
    }
}
