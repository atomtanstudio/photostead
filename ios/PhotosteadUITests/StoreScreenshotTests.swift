// The App Store's screenshots, taken from a real library: run by ios/scripts/screenshots.sh, which
// makes the simulators (6.9" and 6.3", nothing in Photos, the status bar at 9:41) and passes, as
// TEST_RUNNER_… to xcodebuild:
// - SCREENSHOTS_DIR: where the PNGs go (on the Mac; the simulator can write there);
// - PAIR_CODE_SCREENSHOTS and PAIR_ADDRESS: pair first, starting over (a code that lasts does
//   both simulators);
// - SCREENSHOTS_PHOTO: which item the viewer shows, counting from the newest (0 unless set).
// - SCREENSHOTS_ALBUM: an album to show last, by its title (none unless set); when it's shared, its
//   Activity too (the family's comments).
// Without SCREENSHOTS_DIR it's skipped.

import XCTest

final class StoreScreenshotTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    private func element(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    /// The whole screen, status bar included, as the App Store wants it.
    private func shot(_ name: String, in dir: URL) throws {
        let png = XCUIScreen.main.screenshot().pngRepresentation
        try png.write(to: dir.appendingPathComponent("\(name).png"))
        let a = XCTAttachment(data: png, uniformTypeIdentifier: "public.png")
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }

    /// Back to the Library tab's top, as after opening the app.
    private func fresh(_ app: XCUIApplication) {
        app.terminate()
        app.launchArguments = [] // not -reset again: still paired
        app.launch()
        XCTAssertTrue(app.tabBars.buttons["Search"].waitForExistence(timeout: 30), "not paired")
    }

    func testStoreScreenshots() throws {
        guard let path = env["SCREENSHOTS_DIR"] else { throw XCTSkip("SCREENSHOTS_DIR not set") }
        let dir = URL(fileURLWithPath: path)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        XCUIDevice.shared.appearance = .light

        let app = XCUIApplication()
        if let code = env["PAIR_CODE_SCREENSHOTS"], let address = env["PAIR_ADDRESS"] {
            app.launchArguments = ["-reset"]
            app.launch()
            pairByTyping(app, address: address, code: code) // in ViewerTests.swift
        } else {
            app.launch()
        }
        XCTAssertTrue(app.tabBars.buttons["Search"].waitForExistence(timeout: 30), "not paired")

        // The library, then a photo and its details.
        let tiles = app.descendants(matching: .any).matching(identifier: "tile")
        XCTAssertTrue(tiles.firstMatch.waitForExistence(timeout: 120), "the library didn't show")
        Thread.sleep(forTimeInterval: 10) // thumbnails, the memory's picture, and the backup pill settling
        try shot("01 Library", in: dir)
        let n = Int(env["SCREENSHOTS_PHOTO"] ?? "") ?? 0
        tiles.element(boundBy: n).tap()
        XCTAssertTrue(app.buttons["Details"].waitForExistence(timeout: 20), "the viewer didn't open")
        Thread.sleep(forTimeInterval: 3)
        try shot("02 Photo", in: dir)
        app.buttons["Details"].tap()
        XCTAssertTrue(app.navigationBars["Details"].waitForExistence(timeout: 10), "no details")
        Thread.sleep(forTimeInterval: 4) // the server's part, and the map
        try shot("03 Details", in: dir)

        // Search before typing, then the places on a map (or the list, if the server has no map).
        fresh(app)
        app.tabBars.buttons["Search"].tap()
        XCTAssertTrue(element(app, "suggest-place").waitForExistence(timeout: 30), "no suggestions")
        Thread.sleep(forTimeInterval: 3)
        try shot("04 Search", in: dir)
        element(app, "see-all-places").tap()
        // The list (by country, with pictures), even if Places was last left on the map (the app
        // remembers): the map opens to fit every place, which for trips on three continents is
        // mostly ocean.
        let row = element(app, "place"), toggle = app.buttons["places-map"]
        if !row.waitForExistence(timeout: 20), toggle.exists, toggle.label == "Show as a list" { toggle.tap() }
        XCTAssertTrue(row.waitForExistence(timeout: 10), "no places")
        Thread.sleep(forTimeInterval: 2)
        try shot("05 Places", in: dir)

        // Collections, then the album named in SCREENSHOTS_ALBUM, if one is (one with few pictures
        // is mostly empty screen).
        fresh(app)
        app.tabBars.buttons["Collections"].tap()
        Thread.sleep(forTimeInterval: 4)
        try shot("06 Collections", in: dir)
        if let title = env["SCREENSHOTS_ALBUM"], !title.isEmpty {
            app.buttons["All albums"].tap()
            let album = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'album' AND label BEGINSWITH %@", title + ",")).firstMatch
            _ = element(app, "album").waitForExistence(timeout: 20)
            for _ in 0..<8 where !album.isHittable { app.swipeUp() } // the grid makes cards as they come into view
            XCTAssertTrue(album.exists, "no album called \(title)")
            album.tap()
            XCTAssertTrue(tiles.firstMatch.waitForExistence(timeout: 20), "the album didn't open")
            Thread.sleep(forTimeInterval: 3)
            try shot("07 Album", in: dir)
            let activity = element(app, "album-activity")
            if activity.waitForExistence(timeout: 10) {
                activity.tap()
                XCTAssertTrue(element(app, "talk-entry").waitForExistence(timeout: 15), "no activity")
                Thread.sleep(forTimeInterval: 3) // its thumbnails
                try shot("08 Activity", in: dir)
            }
        }
    }
}

// The launch film's phone captures (motiondesignstudio/projects/photostead-launch/tools/capture-phone.sh): that
// script records the simulator's screen while this walks the app slowly, one FILM_PHASE at a time, and opens the
// pairing link (what scanning the web app's QR code opens) when this marks "pair-ready". Every step writes a
// full-resolution still to FILM_DIR and a line to FILM_DIR/marks.txt (seconds since 1970, then the step's name),
// so the recording can be cut at each step. Skipped unless FILM_DIR is set.
final class FilmCaptureTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private var dir: URL!

    private func element(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }
    private func hold(_ s: Double) { Thread.sleep(forTimeInterval: s) }
    private func mark(_ name: String) {
        let line = String(format: "%.3f %@\n", Date().timeIntervalSince1970, name)
        let url = dir.appendingPathComponent("marks.txt")
        if let h = try? FileHandle(forWritingTo: url) { h.seekToEndOfFile(); h.write(line.data(using: .utf8)!); try? h.close() }
        else { try? line.write(to: url, atomically: true, encoding: .utf8) }
    }
    private func shot(_ name: String) {
        mark(name)
        try? XCUIScreen.main.screenshot().pngRepresentation.write(to: dir.appendingPathComponent("\(name).png"))
    }
    /// Typing at a readable pace for the film, one character at a time.
    private func type(_ field: XCUIElement, _ text: String, every: Double = 0.09) {
        for ch in text { field.typeText(String(ch)); hold(every) }
    }

    func testFilm() throws {
        guard let path = env["FILM_DIR"] else { throw XCTSkip("FILM_DIR not set") }
        dir = URL(fileURLWithPath: path)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        XCUIDevice.shared.appearance = .light
        let app = XCUIApplication()
        switch env["FILM_PHASE"] ?? "browse" {
        case "pair": pair(app)
        case "browse": browse(app)
        case "backup": backup(app)
        case "talk": talk(app)
        default: XCTFail("unknown FILM_PHASE")
        }
    }

    private func pair(_ app: XCUIApplication) {
        app.launchArguments = ["-reset"]
        app.launch()
        XCTAssertTrue(app.staticTexts["Pair this iPhone"].waitForExistence(timeout: 30), "no pairing screen")
        hold(2)
        shot("pair-start")
        mark("pair-ready")
        // The link comes from outside the app here, so iOS asks first (scanning the QR code in the app doesn't).
        // (Only the first time: iOS remembers the answer.)
        let open = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Open"]
        let confirm = app.buttons["confirm-pair"]
        let deadline = Date().addingTimeInterval(600)
        while Date() < deadline, !confirm.exists {
            if open.exists { mark("open-asked"); open.tap() }
            hold(0.25)
        }
        XCTAssertTrue(confirm.waitForExistence(timeout: 10), "the pairing link never came")
        hold(2.5)
        shot("pair-confirm")
        confirm.tap()
        let done = app.buttons["welcome-done"], go = app.buttons["welcome-continue"]
        XCTAssertTrue(done.waitForExistence(timeout: 30) || go.exists, "no welcome")
        hold(2)
        shot("welcome")
        if go.exists {
            go.tap()
            let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons.matching(identifier: "Allow Full Access").firstMatch
            if allow.waitForExistence(timeout: 20) { hold(1.5); shot("photos-allow"); allow.tap() }
            _ = done.waitForExistence(timeout: 10)
            hold(1.5)
            shot("welcome-done")
        }
        done.tap()
        XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 120), "the library didn't show")
        hold(10)
        shot("library-paired")
    }

    private func browse(_ app: XCUIApplication) {
        app.launch()
        let tiles = app.descendants(matching: .any).matching(identifier: "tile")
        XCTAssertTrue(tiles.firstMatch.waitForExistence(timeout: 60), "the library didn't show")
        hold(8)
        shot("library-top")
        for i in 1...4 { app.swipeUp(velocity: .slow); hold(2); shot("library-scroll-\(i)") }
        app.tabBars.buttons.element(boundBy: 0).tap() // back to the top
        hold(1); app.tabBars.buttons.element(boundBy: 0).tap()
        hold(3)
        shot("library-back")

        let n = Int(env["FILM_PHOTO"] ?? "") ?? 2
        tiles.element(boundBy: n).tap()
        XCTAssertTrue(app.buttons["Details"].waitForExistence(timeout: 20), "the viewer didn't open")
        hold(3); shot("viewer")
        app.swipeLeft(); hold(2.5); shot("viewer-next")
        app.buttons["Details"].tap()
        XCTAssertTrue(app.navigationBars["Details"].waitForExistence(timeout: 10), "no details")
        hold(4); shot("details")
        app.terminate(); app.launch()
        XCTAssertTrue(tiles.firstMatch.waitForExistence(timeout: 60))

        app.tabBars.buttons["Search"].tap()
        XCTAssertTrue(element(app, "suggest-place").waitForExistence(timeout: 30), "no suggestions")
        hold(3); shot("search-start")
        let field = app.searchFields.firstMatch
        field.tap(); hold(1)
        type(field, env["FILM_QUERY"] ?? "birthday cake", every: 0.12)
        hold(1); shot("search-typed")
        app.keyboards.buttons["search"].tap()
        _ = element(app, "found").waitForExistence(timeout: 30)
        hold(4); shot("search-results")
        if app.buttons["Cancel"].exists { app.buttons["Cancel"].tap() }
        hold(1.5)
        if element(app, "see-all-places").waitForExistence(timeout: 10) {
            element(app, "see-all-places").tap()
            let row = element(app, "place"), toggle = app.buttons["places-map"]
            _ = row.waitForExistence(timeout: 15)
            hold(3); shot("places-a")
            if toggle.exists { toggle.tap(); hold(5); shot("places-b") }
        }
        app.terminate(); app.launch()
        XCTAssertTrue(tiles.firstMatch.waitForExistence(timeout: 60))
        app.tabBars.buttons["Collections"].tap()
        hold(5); shot("collections")
        app.swipeUp(velocity: .slow); hold(2.5); shot("collections-down")
        openAlbum(app, env["FILM_ALBUM"] ?? "Lisbon")
        hold(3); shot("album")
        let activity = element(app, "album-activity")
        if activity.waitForExistence(timeout: 10) {
            activity.tap()
            _ = element(app, "talk-entry").waitForExistence(timeout: 15)
            hold(3); shot("activity")
        }
    }

    private func openAlbum(_ app: XCUIApplication, _ title: String) {
        app.terminate(); app.launch()
        XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 60))
        app.tabBars.buttons["Collections"].tap()
        let album = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'album' AND label BEGINSWITH %@", title + ",")).firstMatch
        _ = element(app, "album").waitForExistence(timeout: 30)
        if app.buttons["All albums"].exists { app.buttons["All albums"].tap() }
        for _ in 0..<8 where !album.isHittable { app.swipeUp() }
        XCTAssertTrue(album.exists, "no album called \(title)")
        album.tap()
        XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 20), "the album didn't open")
    }

    /// Photos were just added to the simulator's library: the app backs them up while this watches.
    private func backup(_ app: XCUIApplication) {
        app.launch()
        XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 60))
        let pill = element(app, "backup-pill")
        for i in 0..<60 {
            shot(String(format: "backup-%02d", i))
            if i > 6, pill.exists, pill.label.contains("Up to date") || pill.label.contains("Backed up") { break }
            hold(0.8)
        }
        hold(2); shot("backup-done")
        if pill.exists { pill.tap(); hold(3); shot("backup-sheet") }
    }

    /// A comment and a pick in the shared album, said from the phone.
    private func talk(_ app: XCUIApplication) {
        openAlbum(app, env["FILM_ALBUM"] ?? "Lisbon")
        let activity = element(app, "album-activity")
        XCTAssertTrue(activity.waitForExistence(timeout: 10), "no Activity: the album isn't shared")
        activity.tap()
        XCTAssertTrue(element(app, "talk-entry").waitForExistence(timeout: 15) || element(app, "talk-input").exists, "no activity")
        hold(3); shot("talk-before")
        let input = element(app, "talk-input")
        input.tap(); hold(1)
        type(input, env["FILM_COMMENT"] ?? "Best day of the whole trip", every: 0.1)
        hold(1); shot("talk-typed")
        element(app, "talk-send").tap()
        hold(3); shot("talk-sent")
    }
}
