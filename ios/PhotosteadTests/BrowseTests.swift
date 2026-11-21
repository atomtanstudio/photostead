import XCTest
@testable import Photostead

final class BrowseTests: XCTestCase {
    private func place(_ name: String, _ country: String?, region: String? = nil, count: Int = 1) -> PlaceRow {
        PlaceRow(id: UUID().uuidString, name: name, region: region, country: country, countryCode: nil, lat: 0, lon: 0, count: count, cover: nil, coverThumbhash: nil)
    }

    /// Countries as the server sends them, each place under its own, and none under Elsewhere, last.
    func testPlacesByCountry() {
        let list = [place("Kyoto", "Japan", count: 9), place("Somewhere", nil), place("Tokyo", "Japan", count: 3),
                    place("Paris", "France"), place("At sea", ""), place("Lyon", "France")]
        let groups = placesByCountry(list)
        XCTAssertEqual(groups.map(\.country), ["Japan", "France", "Elsewhere"])
        XCTAssertEqual(groups[0].places.map(\.name), ["Kyoto", "Tokyo"]) // the server's order kept
        XCTAssertEqual(groups[1].places.map(\.name), ["Paris", "Lyon"])
        XCTAssertEqual(groups[2].places.map(\.name), ["Somewhere", "At sea"])
        XCTAssertTrue(placesByCountry([]).isEmpty)
    }

    private func at(_ name: String, _ lat: Double, _ lon: Double, _ count: Int) -> PlaceRow {
        PlaceRow(id: name, name: name, lat: lat, lon: lon, count: count)
    }

    /// The map opens on home and the places near it, not on every place at once: trips on three
    /// continents would leave a map of ocean.
    func testMapOpensOnHome() throws {
        let home = [at("Portland", 45.52, -122.68, 51), at("Cannon Beach", 45.89, -123.96, 9), at("Mount Hood Village", 45.35, -121.98, 6),
                    at("Odell", 45.63, -121.54, 4), at("Washougal", 45.58, -122.35, 2)]
        let trips = [at("Kyoto", 35.01, 135.77, 5), at("Alfama", 38.71, -9.13, 4), at("Höfn", 64.25, -15.21, 3), at("Vestmannaeyjar", 63.44, -20.27, 3)]
        let r = try XCTUnwrap(placesMapFrame(trips + home))
        for p in home {
            XCTAssertLessThan(abs(p.lat - r.center.latitude), r.span.latitudeDelta / 2, p.name)
            XCTAssertLessThan(abs(p.lon - r.center.longitude), r.span.longitudeDelta / 2, p.name)
        }
        XCTAssertLessThan(r.span.longitudeDelta, 5) // Oregon, not the Pacific
        XCTAssertGreaterThan(abs(trips[0].lon - r.center.longitude), r.span.longitudeDelta)

        // A library that stays near home frames all of it.
        let local = try XCTUnwrap(placesMapFrame(home))
        XCTAssertEqual(local.center.longitude, (-123.96 + -121.54) / 2, accuracy: 0.001)
        // Home is where the pictures are, not where the most places are: one busy town beats three quiet ones.
        let busy = try XCTUnwrap(placesMapFrame([at("A", 10, 10, 1), at("B", 10.5, 10.5, 1), at("C", 11, 10, 1), at("Busy", -30, 150, 40)]))
        XCTAssertEqual(busy.center.latitude, -30, accuracy: 0.001)
        XCTAssertEqual(busy.span.latitudeDelta, 0.3, accuracy: 0.001) // one place: its surroundings
        // Across the date line, Fiji's islands are neighbours, and the map opens between them.
        let fiji = try XCTUnwrap(placesMapFrame([at("Taveuni", -16.8, 179.9, 5), at("Vanua Balavu", -17.2, -178.9, 4)]))
        XCTAssertEqual(fiji.span.longitudeDelta, 1.2 * 1.5, accuracy: 0.001)
        XCTAssertEqual(abs(fiji.center.longitude), 179.5, accuracy: 0.001)
        XCTAssertNil(placesMapFrame([]))
    }

    /// Under a place's name: its region only when it says more than the name, and how many.
    func testPlaceLine() {
        XCTAssertEqual(place("Redmond", "United States", region: "Washington", count: 6).line, "Washington · 6 items")
        XCTAssertEqual(place("Kyoto", "Japan", region: "Kyoto", count: 1).line, "1 item")
        XCTAssertEqual(place("Paris", "France", count: 1200).line, "\(1200.formatted()) items")
    }

    /// The server's places, with a map when it shows one and without when it's off.
    func testPlacesAsTheServerSendsThem() throws {
        let on = """
        {"places":[{"id":"01a1bc5b-a002-756a-ac7d-b510acf29b5b","name":"Redmond","region":"Washington","country":"United States",
        "countryCode":"US","lat":47.67399,"lon":-122.12151,"count":6,"cover":"01a14823-89a1-7c91-9be0-782b5798b38c",
        "coverThumbhash":"XggODYKIeIh/iHeKd5d5entGgEkF"}],
        "map":{"light":"https://tiles.openfreemap.org/styles/positron","dark":"https://tiles.openfreemap.org/styles/dark"},
        "attribution":"Places from GeoNames (CC BY 4.0)"}
        """
        let list = try JSONDecoder.photostead.decode(PlacesList.self, from: Data(on.utf8))
        XCTAssertNotNil(list.map)
        XCTAssertEqual(list.places.first?.name, "Redmond")
        XCTAssertEqual(list.places.first?.count, 6)
        XCTAssertEqual(list.places.first?.coverThumbhash?.count, 21)
        let off = #"{"places":[{"id":"x","name":"Somewhere","lat":1.5,"lon":2,"count":1}],"attribution":"Places from GeoNames (CC BY 4.0)"}"#
        let without = try JSONDecoder.photostead.decode(PlacesList.self, from: Data(off.utf8))
        XCTAssertNil(without.map)
        XCTAssertNil(without.places.first?.country)
        XCTAssertEqual(placesByCountry(without.places).first?.country, "Elsewhere")
    }

    func testCharactersAndPrompts() throws {
        let characters = """
        {"characters":[{"id":"c1","name":"Daniela","patterns":["^daniela$"],"count":10,"firstDate":"2024-01-02","lastDate":"2024-03-04",
        "samples":[{"id":"s1","thumbhash":"XggODYKIeIh/iHeKd5d5entGgEkF"},{"id":"s2"}],"loras":["daniela_sdxl"],"faceGroups":0},
        {"id":"c2","name":"Mia","patterns":[],"count":0,"samples":[],"loras":[],"faceGroups":1}]}
        """
        struct Result: Decodable { var characters: [CharacterRow] }
        let list = try JSONDecoder.photostead.decode(Result.self, from: Data(characters.utf8)).characters
        XCTAssertEqual(list.map(\.name), ["Daniela", "Mia"])
        XCTAssertEqual(list[0].samples.count, 2)
        XCTAssertNil(list[0].samples[1].thumbhash)
        let prompts = #"{"total":312,"prompts":[{"id":"p1","text":"a lighthouse at dusk","uses":219,"firstDate":"2022-11-04","samples":[{"id":"s1"}],"models":[],"loras":[]}]}"#
        struct Prompts: Decodable { var prompts: [PromptRow]; var total: Int }
        let p = try JSONDecoder.photostead.decode(Prompts.self, from: Data(prompts.utf8))
        XCTAssertEqual(p.total, 312)
        XCTAssertEqual(p.prompts.first?.uses, 219)
    }

    /// A route to a place, a character or a month is a value NavigationStack can keep.
    func testRoutesAreValues() {
        let p = place("Kyoto", "Japan")
        XCTAssertEqual(Route.place(p), Route.place(p))
        XCTAssertNotEqual(Route.month("2026-08"), Route.month("2026-07"))
        XCTAssertNotEqual(Route.places, Route.characters)
    }

    /// Things to try are searches by meaning: each once, each with a symbol iOS has.
    func testThingsToTry() {
        XCTAssertEqual(Set(thingsToTry.map(\.q)).count, thingsToTry.count)
        for t in thingsToTry { XCTAssertNotNil(UIImage(systemName: t.symbol), t.symbol) }
        for l in utilityLabels { XCTAssertNotNil(UIImage(systemName: l.symbol), l.symbol) }
    }
}
