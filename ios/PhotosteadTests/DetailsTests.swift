@testable import Photostead
import XCTest

/// An item's details in words (DetailText), and reading them from the server (ItemDetails).
final class DetailsTests: XCTestCase {
    private let us = Locale(identifier: "en_US")

    func testExposure() {
        XCTAssertEqual(DetailText.aperture(1.78, locale: us), "ƒ/1.78")
        XCTAssertEqual(DetailText.aperture(8, locale: us), "ƒ/8")
        XCTAssertEqual(DetailText.aperture(2.2, locale: us), "ƒ/2.2")

        XCTAssertEqual(DetailText.shutter(0.004, locale: us), "1/250 s")
        XCTAssertEqual(DetailText.shutter(0.016666668, locale: us), "1/60 s") // as a float32 comes back
        XCTAssertEqual(DetailText.shutter(0.008333334, locale: us), "1/120 s")
        XCTAssertEqual(DetailText.shutter(0.5, locale: us), "1/2 s")
        XCTAssertEqual(DetailText.shutter(0.6, locale: us), "0.6 s") // not near a whole fraction
        XCTAssertEqual(DetailText.shutter(2, locale: us), "2 s")
        XCTAssertEqual(DetailText.shutter(1.5, locale: us), "1.5 s")
        XCTAssertNil(DetailText.shutter(0))

        XCTAssertEqual(DetailText.focal(6.765, locale: us), "6.8 mm")
        XCTAssertEqual(DetailText.focal(50, locale: us), "50 mm")
        XCTAssertEqual(DetailText.focal(23.6, locale: us), "24 mm")
        XCTAssertNil(DetailText.focal(0))

        let camera = ItemDetails.Camera(make: "Apple", model: "iPhone 15 Pro", lens: nil, focalMm: 6.765, fNumber: 1.78, exposureS: 0.008333334, iso: 64)
        XCTAssertEqual(DetailText.exposure(camera, locale: us), ["ƒ/1.78", "1/120 s", "6.8 mm", "ISO 64"])
        let partial = ItemDetails.Camera(make: "Nikon", model: nil, lens: nil, focalMm: nil, fNumber: 0, exposureS: nil, iso: 400)
        XCTAssertEqual(DetailText.exposure(partial, locale: us), ["ISO 400"])
    }

    func testCameraAndLens() {
        XCTAssertEqual(DetailText.camera(make: "Apple", model: "iPhone 15 Pro"), "Apple iPhone 15 Pro")
        XCTAssertEqual(DetailText.camera(make: "Canon", model: "Canon EOS R5"), "Canon EOS R5")
        XCTAssertEqual(DetailText.camera(make: "NIKON CORPORATION", model: "NIKON Z 6"), "NIKON CORPORATION NIKON Z 6")
        XCTAssertEqual(DetailText.camera(make: "Nikon", model: nil), "Nikon")
        XCTAssertNil(DetailText.camera(make: nil, model: " "))

        XCTAssertEqual(DetailText.lens("iPhone 15 Pro back triple camera 6.765mm f/1.78", model: "iPhone 15 Pro"), "Back triple camera 6.765mm f/1.78")
        XCTAssertEqual(DetailText.lens("RF24-105mm F4 L IS USM", model: "Canon EOS R5"), "RF24-105mm F4 L IS USM")
        XCTAssertEqual(DetailText.lens("iPhone 15 Pro", model: "iPhone 15 Pro"), "iPhone 15 Pro") // nothing left: as it was
    }

    func testSizeAndFormat() {
        func size(_ w: Int, _ h: Int, video: Bool = false, ms: Int? = nil, bytes: Int64? = nil) -> String {
            DetailText.size(width: w, height: h, video: video, durationMs: ms, bytes: bytes, locale: us)
        }
        let nbsp = "\u{00A0}"
        let photo = size(4032, 3024, bytes: 2_100_000)
        XCTAssertTrue(photo.hasPrefix("12\(nbsp)MP · 4032\(nbsp)×\(nbsp)3024 · "), photo) // each part kept on one line
        XCTAssertEqual(size(1600, 1200).replacingOccurrences(of: nbsp, with: " "), "1.9 MP · 1600 × 1200")
        XCTAssertEqual(size(1920, 1080, video: true, ms: 229_576).replacingOccurrences(of: nbsp, with: " "), "1920 × 1080 · 3:49") // a video's length, no megapixels
        XCTAssertEqual(size(0, 0), "")
        XCTAssertEqual(DetailText.together(["ƒ/1.78", "6.8 mm", "ISO 64"]), "ƒ/1.78 · 6.8\(nbsp)mm · ISO\(nbsp)64")

        XCTAssertEqual(DetailText.format(fileName: "IMG_8801.JPG"), "JPEG")
        XCTAssertEqual(DetailText.format(fileName: "IMG_0001.heic"), "HEIF")
        XCTAssertEqual(DetailText.format(fileName: "DSC_0001.NEF"), "RAW")
        XCTAssertEqual(DetailText.format(fileName: "clip.mov"), "MOV")
        XCTAssertNil(DetailText.format(fileName: ""))
    }

    func testWhereAndWhen() {
        XCTAssertEqual(DetailText.coordinates(lat: 47.6295, lon: -122.3599, locale: us), "47.6295° N, 122.3599° W")
        XCTAssertEqual(DetailText.coordinates(lat: -33.8688, lon: 151.2093, locale: us), "33.8688° S, 151.2093° E")

        // 23:30 in Tokyo is 07:30 in Seattle the same day: the clock where it was taken, with its offset.
        let taken = ISO8601DateFormatter().date(from: "2024-05-01T14:30:00Z")!
        let seattle = TimeZone(identifier: "America/Los_Angeles")!
        XCTAssertEqual(DetailText.time(taken, offsetMin: 540, phone: seattle, locale: us).replacingOccurrences(of: "\u{202F}", with: " "), "11:30 PM · UTC+9")
        XCTAssertEqual(DetailText.day(taken, offsetMin: 540, phone: seattle, locale: us), "Wednesday, May 1, 2024")
        // Taken where the phone is: no offset to show.
        XCTAssertEqual(DetailText.time(taken, offsetMin: -420, phone: seattle, locale: us).replacingOccurrences(of: "\u{202F}", with: " "), "7:30 AM")
        // Not said: the phone's clock.
        XCTAssertEqual(DetailText.time(taken, offsetMin: nil, phone: seattle, locale: us).replacingOccurrences(of: "\u{202F}", with: " "), "7:30 AM")
        XCTAssertEqual(DetailText.time(taken, offsetMin: 330, phone: seattle, locale: us).replacingOccurrences(of: "\u{202F}", with: " "), "8:00 PM · UTC+5:30")

        XCTAssertNil(DetailText.dateNote("exif-original"))
        XCTAssertNotNil(DetailText.dateNote("file-modified"))
    }

    func testEvidence() {
        var g = ItemDetails.Generation(kind: "generated", source: "c2pa", evidence: "c2pa", tool: "Google AI")
        XCTAssertEqual(DetailText.evidence(g), "From the file’s Content Credentials (C2PA), made by Google AI.")
        g = ItemDetails.Generation(kind: "generated", source: "comfyui", evidence: "video:comment")
        XCTAssertEqual(DetailText.evidence(g), "From the ComfyUI workflow saved in the video.")
    }

    /// As the server sends it (GET /api/v1/assets/{id}), trimmed.
    func testReadingDetails() throws {
        let json = """
        {"id":"01a1d54c-e283-7d7e-8266-f62cc721a5c1","type":"image","takenAt":"2026-11-12T17:05:00-08:00","localDate":"2026-11-12",
         "width":1440,"height":1080,"thumbhash":"XggODYKIeIh/iHeKd5d5entGgEkF","render":"done","mime":"image/jpeg","offsetMin":-480,
         "dateSource":"exif-original","sourceType":"upload","fileName":"IMG_8801.JPG","folderPath":"photos/2026/2026-11-13","library":"uploads",
         "fileSize":27660,"camera":{"make":"Apple","model":"iPhone 15 Pro","lens":"iPhone 15 Pro back triple camera 6.765mm f/1.78",
         "focalMm":6.765,"fNumber":1.78,"exposureS":0.008333334,"iso":64},
         "location":{"lat":47.6297,"lon":-122.3601,"place":"Seattle, Washington, United States","placeId":"01a1d54d-7ba0-72e1-8cdb-ac359fc49b07"},
         "albums":[],"people":[{"id":"p1","name":"Mia","source":"faces","kind":"human"},{"id":"p2","name":"","kind":"pet"}],
         "characters":[],"labels":[],"keywords":["sunset"],
         "generation":{"kind":"generated","source":"a1111","evidence":"png:parameters","tool":"Automatic1111","model":"juggernautXL",
          "modelId":"m1","loras":[{"id":"l1","name":"Mina_epoch_10","weight":1}],"prompt":"a wall","promptUses":1,"seed":"42","steps":32,"cfg":4,
          "settings":{"clip skip":"2"},"hasWorkflow":false}}
        """
        let d = try JSONDecoder.photostead.decode(ItemDetails.self, from: Data(json.utf8))
        XCTAssertEqual(d.fileName, "IMG_8801.JPG")
        XCTAssertEqual(d.offsetMin, -480)
        XCTAssertEqual(d.camera?.iso, 64)
        XCTAssertEqual(d.location?.place, "Seattle, Washington, United States")
        XCTAssertEqual(d.people?.map(\.kind), ["human", "pet"])
        XCTAssertEqual(d.generation?.loras?.first?.weight, 1)
        XCTAssertEqual(d.generation?.steps, 32)
        XCTAssertEqual(d.takenAt, ISO8601DateFormatter().date(from: "2026-11-13T01:05:00Z"))
    }
}
