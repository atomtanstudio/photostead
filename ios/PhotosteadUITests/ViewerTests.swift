// The viewer against a real server: an item's details (a Live Photo, a photo with a camera, a place,
// a description and keywords, a video, an AI picture), a Live Photo playing, and the library's tiles
// at the largest text size. Needs PAIR_CODE_VIEWER and PAIR_ADDRESS (pass them as TEST_RUNNER_… to
// xcodebuild), a library whose newest items are that Live Photo, photo and video, and AI_NAME, the
// file name of an AI picture to search for. Run it once with the Simulator light and once dark
// (simctl ui … appearance; MODE names the screenshots): an appearance set from the test reaches the
// app only after a relaunch. testPressToPlay runs after it, still paired, and holds a Live Photo down
// for a screenshot taken from outside (simctl io) meanwhile.

import XCTest

final class ViewerTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    override func setUp() {
        continueAfterFailure = true
    }

    private func shot(_ app: XCUIApplication, _ name: String) {
        let s = XCTAttachment(screenshot: app.screenshot())
        s.name = name
        s.lifetime = .keepAlways
        add(s)
    }

    private func pair(_ app: XCUIApplication, code: String, address: String) {
        pairByTyping(app, address: address, code: code)
    }

    private func firstTile(_ app: XCUIApplication) -> XCUIElement {
        let tile = app.descendants(matching: .any).matching(identifier: "tile").firstMatch
        XCTAssertTrue(tile.waitForExistence(timeout: 120), "the library didn't show")
        return tile
    }

    /// The details sheet, at half height, then full, then scrolled to its end.
    private func details(_ app: XCUIApplication, _ name: String) {
        app.buttons["Details"].tap()
        let bar = app.navigationBars["Details"]
        XCTAssertTrue(bar.waitForExistence(timeout: 10), "no details")
        Thread.sleep(forTimeInterval: 3) // the server's part, the map
        shot(app, "\(name) details")
        bar.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.2))
            .press(forDuration: 0.1, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.02)))
        Thread.sleep(forTimeInterval: 2)
        shot(app, "\(name) details, full")
        let list = app.collectionViews.firstMatch
        if list.exists {
            list.swipeUp()
            Thread.sleep(forTimeInterval: 1)
            shot(app, "\(name) details, further")
        }
        app.buttons["Done"].firstMatch.tap()
        Thread.sleep(forTimeInterval: 1)
    }

    private func walk(_ app: XCUIApplication, _ mode: String) {
        let tile = firstTile(app)
        Thread.sleep(forTimeInterval: 3) // thumbnails
        shot(app, "library, \(mode)")

        tile.tap() // the newest: the Live Photo
        XCTAssertTrue(app.buttons["close"].waitForExistence(timeout: 10), "the viewer didn't open")
        Thread.sleep(forTimeInterval: 2)
        let live = app.buttons["live"]
        XCTAssertTrue(live.waitForExistence(timeout: 5), "no LIVE badge on the Live Photo")
        shot(app, "live photo, \(mode)")
        live.tap()
        Thread.sleep(forTimeInterval: 1.2)
        shot(app, "live photo playing, \(mode)")
        Thread.sleep(forTimeInterval: 3) // to its end, and back to the photo
        shot(app, "live photo after, \(mode)")
        details(app, "live photo, \(mode)")

        app.swipeLeft() // the photo with everything
        Thread.sleep(forTimeInterval: 2)
        details(app, "photo, \(mode)")

        app.swipeLeft() // the video
        Thread.sleep(forTimeInterval: 2)
        details(app, "video, \(mode)")
        app.buttons["close"].tap()

        if let ai = env["AI_NAME"] {
            app.tabBars.buttons["Search"].tap()
            let field = app.searchFields.firstMatch
            XCTAssertTrue(field.waitForExistence(timeout: 5))
            field.tap()
            field.typeText(ai + "\n")
            let found = app.descendants(matching: .any).matching(identifier: "tile").firstMatch
            if found.waitForExistence(timeout: 20) {
                found.tap()
                XCTAssertTrue(app.buttons["close"].waitForExistence(timeout: 10))
                Thread.sleep(forTimeInterval: 2)
                details(app, "ai, \(mode)")
                app.buttons["close"].tap()
            } else {
                XCTFail("the AI picture wasn't found")
            }
            if app.buttons["Cancel"].exists { app.buttons["Cancel"].tap() }
            app.tabBars.buttons["Library"].tap()
        }
    }

    func testDetailsAndLivePhotos() throws {
        guard let code = env["PAIR_CODE_VIEWER"], let address = env["PAIR_ADDRESS"] else { throw XCTSkip("needs PAIR_CODE_VIEWER and PAIR_ADDRESS") }
        let mode = env["MODE"] ?? "light"
        let app = XCUIApplication()
        app.launchArguments = ["-reset"]
        app.launch()
        pair(app, code: code, address: address)
        walk(app, mode)
        app.terminate()

        // The largest text: tiles (a favorite video's heart and length), the viewer and details.
        let big = XCUIApplication()
        big.launchArguments = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXL"]
        big.launch()
        let tile = firstTile(big)
        Thread.sleep(forTimeInterval: 3)
        shot(big, "largest text, library, \(mode)")
        tile.tap()
        XCTAssertTrue(big.buttons["close"].waitForExistence(timeout: 10))
        Thread.sleep(forTimeInterval: 2)
        shot(big, "largest text, live photo, \(mode)")
        big.swipeLeft()
        Thread.sleep(forTimeInterval: 2)
        details(big, "largest text, photo, \(mode)")
        big.buttons["close"].tap()
    }

    /// Still paired: the LIVE badge's menu (sound), and someone in a picture opening their items from
    /// its details. PERSON_ITEM is the file name of an item with someone in it.
    func testLiveMenuAndPeople() throws {
        guard let name = env["PERSON_ITEM"] else { throw XCTSkip("needs PERSON_ITEM (and the app paired)") }
        let mode = env["MODE"] ?? "light"
        let app = XCUIApplication()
        app.launch()
        firstTile(app).tap()
        let live = app.buttons["live"]
        XCTAssertTrue(live.waitForExistence(timeout: 10))
        live.press(forDuration: 1.0)
        Thread.sleep(forTimeInterval: 1)
        shot(app, "live menu, \(mode)")
        XCTAssertTrue(app.buttons["Sound"].exists || app.switches["Sound"].exists, "no Sound in the LIVE menu")
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.85)).tap() // away
        Thread.sleep(forTimeInterval: 0.5)
        app.buttons["close"].tap()

        app.tabBars.buttons["Search"].tap()
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        field.tap()
        field.typeText(name + "\n")
        let found = app.descendants(matching: .any).matching(identifier: "tile").firstMatch
        XCTAssertTrue(found.waitForExistence(timeout: 20), "the item wasn't found")
        found.tap()
        XCTAssertTrue(app.buttons["close"].waitForExistence(timeout: 10))
        app.buttons["Details"].tap()
        let bar = app.navigationBars["Details"]
        XCTAssertTrue(bar.waitForExistence(timeout: 10))
        Thread.sleep(forTimeInterval: 3)
        bar.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.2))
            .press(forDuration: 0.1, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.02)))
        Thread.sleep(forTimeInterval: 1.5)
        app.collectionViews.firstMatch.swipeUp()
        Thread.sleep(forTimeInterval: 1)
        shot(app, "person details, \(mode)")
        let person = app.collectionViews.firstMatch.buttons.containing(NSPredicate(format: "label CONTAINS[c] %@", env["PERSON_NAME"] ?? "")).firstMatch
        if person.exists {
            person.tap()
            Thread.sleep(forTimeInterval: 4)
            shot(app, "person from details, \(mode)")
        } else {
            XCTFail("no person in the details")
        }
    }

    /// The made-up library (no server) at the largest text, four across and pinched to seven: its
    /// first tile is a favorite video, whose heart and length once overlapped. Unpairs (-reset).
    /// Light or dark as the Simulator is (MODE names the screenshots).
    func testTilesAtLargestText() throws {
        for columns in [4, 7] {
            let app = XCUIApplication()
            app.launchArguments = ["-reset", "-demoLibrary", "3000", "-columns", "\(columns)",
                                   "-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXL"]
            app.launch()
            _ = firstTile(app)
            Thread.sleep(forTimeInterval: 2)
            shot(app, "demo tiles, largest text, \(columns) across, \(env["MODE"] ?? "light")")
            app.terminate()
        }
    }

    /// Adult content blurred, then hidden, from the phone's Settings: needs PAIR_CODE_ADULT and
    /// PAIR_ADDRESS, and a library whose second and third newest items are adult (a photo, a video).
    /// Leaves the setting on Show.
    func testAdultBlurredAndHidden() throws {
        guard let code = env["PAIR_CODE_ADULT"], let address = env["PAIR_ADDRESS"] else { throw XCTSkip("needs PAIR_CODE_ADULT and PAIR_ADDRESS") }
        let mode = env["MODE"] ?? "light"
        let app = XCUIApplication()
        app.launchArguments = ["-reset"]
        app.launch()
        pair(app, code: code, address: address)
        let tiles = app.descendants(matching: .any).matching(identifier: "tile")
        XCTAssertTrue(tiles.firstMatch.waitForExistence(timeout: 120), "the library didn't show")
        let before = tiles.count

        setAdult(app, "Blur")
        let blurred = tiles.matching(NSPredicate(format: "label BEGINSWITH 'Adult content'"))
        XCTAssertTrue(blurred.firstMatch.waitForExistence(timeout: 10), "no blurred tile")
        XCTAssertEqual(blurred.count, 2)
        Thread.sleep(forTimeInterval: 3)
        shot(app, "adult blurred, library, \(mode)")

        blurred.firstMatch.tap()
        // The pages beside are there too, off the screen: the one in view is the one in the middle.
        let shows = app.buttons.matching(identifier: "show-adult")
        let show = { shows.allElementsBoundByIndex.first { app.frame.contains(CGPoint(x: $0.frame.midX, y: $0.frame.midY)) } }
        XCTAssertTrue(shows.firstMatch.waitForExistence(timeout: 10), "no Show over an adult item")
        Thread.sleep(forTimeInterval: 2)
        XCTAssertNotNil(show(), "no Show in view")
        shot(app, "adult veiled, viewer, \(mode)")
        show()?.tap()
        Thread.sleep(forTimeInterval: 2)
        XCTAssertNil(show(), "still veiled after Show")
        shot(app, "adult shown, viewer, \(mode)")
        app.swipeLeft() // the adult video: veiled, not playing
        Thread.sleep(forTimeInterval: 2)
        XCTAssertNotNil(show(), "the adult video isn't veiled")
        shot(app, "adult video veiled, viewer, \(mode)")
        app.buttons["close"].tap()
        XCTAssertEqual(blurred.count, 1, "the one shown is still blurred in the library")

        setAdult(app, "Hide")
        let fewer = NSPredicate(format: "count == %d", before - 2)
        expectation(for: fewer, evaluatedWith: tiles)
        waitForExpectations(timeout: 10)
        shot(app, "adult hidden, library, \(mode)")

        setAdult(app, "Show")
        XCTAssertTrue(tiles.element(boundBy: before - 1).waitForExistence(timeout: 10), "hidden items didn't come back")
        XCTAssertEqual(blurred.count, 0)
    }

    /// Settings → Adult content → mode, and back to the library.
    private func setAdult(_ app: XCUIApplication, _ mode: String) {
        app.buttons["account"].firstMatch.tap()
        let picker = app.buttons["adult-mode"]
        if !picker.waitForExistence(timeout: 10) { app.swipeUp() }
        XCTAssertTrue(picker.waitForExistence(timeout: 5), "no Adult content in Settings")
        picker.tap()
        let choice = app.buttons[mode].firstMatch
        XCTAssertTrue(choice.waitForExistence(timeout: 5), "no \(mode) in the menu")
        choice.tap()
        Thread.sleep(forTimeInterval: 2) // the server's answer
        shot(app, "settings, adult \(mode)")
        app.buttons["Done"].firstMatch.tap()
        Thread.sleep(forTimeInterval: 1)
    }

    /// Holding the Live Photo down plays it; letting go puts the photo back. Prints when it's held, so
    /// a screenshot can be taken from outside meanwhile.
    func testPressToPlay() throws {
        guard env["PRESS"] != nil else { throw XCTSkip("needs PRESS (and the app paired)") }
        let app = XCUIApplication()
        app.launch()
        firstTile(app).tap()
        XCTAssertTrue(app.buttons["close"].waitForExistence(timeout: 10))
        Thread.sleep(forTimeInterval: 3) // the clip loads while the photo is shown
        let shown = app.descendants(matching: .any).matching(identifier: "shown").firstMatch
        print("PHOTOSTEAD-PRESSING \(Date().timeIntervalSince1970)")
        shown.press(forDuration: 2.5)
        print("PHOTOSTEAD-LET-GO \(Date().timeIntervalSince1970)")
        Thread.sleep(forTimeInterval: 0.6)
        shot(app, "after the press")
        // A tap still hides and shows the bars; a swipe still goes to the next.
        shown.tap()
        Thread.sleep(forTimeInterval: 0.6)
        XCTAssertFalse(app.buttons["close"].isHittable, "a tap on a Live Photo didn't hide the bars")
        shown.tap()
        Thread.sleep(forTimeInterval: 0.6)
        XCTAssertTrue(app.buttons["close"].waitForExistence(timeout: 2))
        app.swipeLeft()
        Thread.sleep(forTimeInterval: 1.5)
        XCTAssertFalse(app.buttons["live"].exists, "still on the Live Photo after a swipe")
        shot(app, "swiped on")
    }
}

/// Pairing on the pairing screen as it is now: the typed path is behind "type instead", and a fresh
/// pairing goes through the first run (allowing Photos if iOS asks).
func pairByTyping(_ app: XCUIApplication, address: String, code: String) {
    let instead = app.buttons["type-instead"]
    if instead.waitForExistence(timeout: 10) { instead.tap() }
    let field = app.textFields["address"]
    XCTAssertTrue(field.waitForExistence(timeout: 10))
    field.tap()
    field.typeText(address)
    let codeField = app.textFields["code"]
    codeField.tap()
    codeField.typeText(code)
    app.buttons["pair"].tap()
    let done = app.buttons["welcome-done"], go = app.buttons["welcome-continue"]
    guard done.waitForExistence(timeout: 20) || go.exists else { return }
    if go.exists {
        go.tap()
        // Two match when Photos is empty (the prompt shows no sample of the library).
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons.matching(identifier: "Allow Full Access").firstMatch
        if allow.waitForExistence(timeout: 20) { allow.tap() } // slow on a simulator just booted
        _ = done.waitForExistence(timeout: 10)
    }
    done.tap()
}
