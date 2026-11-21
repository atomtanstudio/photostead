// Browsing a real library, against a test server: Search before typing (people, places, months,
// things to try, the labels), Places as a list and on the map, a place's items, Collections, and
// making the first album. One run per look: set the simulator's appearance first (xcrun simctl ui
// … appearance dark), and pass, as TEST_RUNNER_… to xcodebuild:
// - BROWSE_MODE: names the screenshots (light, dark, largest text);
// - BROWSE_LARGEST=1: the largest text size;
// - BROWSE_EXTRAS=1: the map, characters, prompts, Recently added and a search too;
// - BROWSE_ONLY=collections: only Collections;
// - PAIR_CODE_BROWSING and PAIR_ADDRESS: pair first (starting over); without them it's paired already.
// - SMART_ALBUM: a smart album's title, for testSmartAlbum (skipped without).
// - TALK_ALBUM: a shared album's title (with comments and picks), for testSharedAlbumTalk (skipped
//   without). Run it paired as its owner and as someone it's shared with: what it adds, it takes back.
// testFirstAlbum makes an album on the server ("Redmond walk"): delete it afterwards.

import XCTest

final class BrowsingTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private var mode: String { env["BROWSE_MODE"] ?? "light" }

    private func shot(_ app: XCUIApplication, _ name: String) {
        let s = XCTAttachment(screenshot: app.screenshot())
        s.name = "\(name) (\(mode))"
        s.lifetime = .keepAlways
        add(s)
    }

    private func element(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    private func back(_ app: XCUIApplication) {
        app.navigationBars.buttons.element(boundBy: 0).tap()
        Thread.sleep(forTimeInterval: 1)
    }

    /// Whether the tab bar comes into sight (or goes out of it) within a few seconds.
    private func tabBar(_ app: XCUIApplication, shown: Bool) -> Bool {
        let seen = NSPredicate(format: "hittable == %@", NSNumber(value: shown))
        return XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: seen, object: app.tabBars.buttons["Collections"])], timeout: 5) == .completed
    }

    private func tab(_ app: XCUIApplication, _ name: String) {
        let button = app.tabBars.buttons[name]
        XCTAssertTrue(button.waitForExistence(timeout: 20), "no \(name) tab")
        button.tap()
    }

    /// Paired (with a code, starting over) or as it was, at the text size asked for.
    private func launch() -> XCUIApplication {
        addUIInterruptionMonitor(withDescription: "permissions") { alert in
            for b in ["Don’t Allow", "Don't Allow"] where alert.buttons[b].exists {
                alert.buttons[b].tap()
                return true
            }
            return false
        }
        // Said here, not only with simctl: a test session keeps the look the last one set.
        XCUIDevice.shared.appearance = mode.contains("dark") ? .dark : .light
        let app = XCUIApplication()
        let code = env["PAIR_CODE_BROWSING"], address = env["PAIR_ADDRESS"]
        if code != nil { app.launchArguments = ["-reset"] }
        if env["BROWSE_LARGEST"] == "1" { app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXL"] }
        app.launch()
        if let code, let address { pairByTyping(app, address: address, code: code) } // in ViewerTests.swift
        XCTAssertTrue(app.tabBars.buttons["Search"].waitForExistence(timeout: 30), "not paired")
        return app
    }

    func testBrowsing() throws {
        let app = launch()
        let extras = env["BROWSE_EXTRAS"] == "1"
        if env["BROWSE_ONLY"] != "collections" {
            tab(app, "Search")
            XCTAssertTrue(element(app, "suggest-place").waitForExistence(timeout: 30), "no places to suggest")
            Thread.sleep(forTimeInterval: 3) // thumbnails
            shot(app, "search before typing")
            app.swipeUp()
            Thread.sleep(forTimeInterval: 1)
            shot(app, "search before typing, further down")
            app.swipeUp()
            Thread.sleep(forTimeInterval: 1)
            shot(app, "search before typing, the end")
            app.swipeDown()
            app.swipeDown()
            app.swipeDown()

            let all = element(app, "see-all-places")
            XCTAssertTrue(all.waitForExistence(timeout: 5))
            all.tap()
            XCTAssertTrue(element(app, "place").waitForExistence(timeout: 20), "no places listed")
            Thread.sleep(forTimeInterval: 2)
            shot(app, "places")
            if extras, app.buttons["places-map"].exists {
                app.buttons["places-map"].tap()
                Thread.sleep(forTimeInterval: 10) // Apple's tiles
                shot(app, "places on the map")
                let pin = element(app, "place-pin")
                if pin.exists {
                    pin.tap()
                    XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 20), "a pin didn't open its place")
                    back(app)
                }
                app.buttons["places-map"].tap() // back to the list (it's remembered)
                Thread.sleep(forTimeInterval: 1)
            }
            element(app, "place").tap()
            XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 20), "the place's items didn't show")
            Thread.sleep(forTimeInterval: 3)
            shot(app, "a place's items")
            back(app)
            back(app)

            let month = element(app, "suggest-month")
            if month.waitForExistence(timeout: 3) {
                month.tap()
                XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 30), "the month's items didn't show")
                Thread.sleep(forTimeInterval: 3)
                shot(app, "a month")
                back(app)
            }
        }

        tab(app, "Collections")
        Thread.sleep(forTimeInterval: 3)
        shot(app, "collections")
        app.swipeUp()
        Thread.sleep(forTimeInterval: 1)
        shot(app, "collections, further down")
        app.swipeDown()
        app.swipeDown()
        let album = element(app, "album")
        if album.exists { // its card opens it
            album.tap()
            XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 20), "the album didn't open")
            Thread.sleep(forTimeInterval: 2)
            shot(app, "an album")
            back(app)
        }
        guard extras else { return }
        for (row, id, name) in [("collection-Characters", "character", "characters"), ("collection-Prompts", "prompt", "prompts"),
                                ("collection-Recently added", "tile", "recently added")] {
            let r = element(app, row)
            if !r.isHittable { app.swipeUp() }
            guard r.exists else { continue }
            r.tap()
            XCTAssertTrue(element(app, id).waitForExistence(timeout: 20), "\(name) didn't show")
            Thread.sleep(forTimeInterval: 3)
            shot(app, name)
            back(app)
        }

        // A thing to try runs a search; then, typing in an empty box, recent searches come first.
        tab(app, "Search")
        let thing = element(app, "suggest-thing")
        if !thing.waitForExistence(timeout: 5) { return }
        app.swipeUp()
        thing.tap()
        XCTAssertTrue(element(app, "found").waitForExistence(timeout: 30))
        Thread.sleep(forTimeInterval: 2)
        shot(app, "a thing to try, searched")
        app.searchFields.firstMatch.buttons.firstMatch.tap() // empties the box, still typing
        Thread.sleep(forTimeInterval: 1)
        shot(app, "search while typing, recent first")
    }

    /// A smart album opens on its items, which the server picks by its rules.
    func testSmartAlbum() throws {
        guard let title = env["SMART_ALBUM"], !title.isEmpty else { throw XCTSkip("SMART_ALBUM isn't set") }
        let app = launch()
        tab(app, "Collections")
        app.buttons["All albums"].tap()
        let album = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'album' AND label BEGINSWITH %@", title + ",")).firstMatch
        _ = element(app, "album").waitForExistence(timeout: 20)
        for _ in 0..<8 where !album.isHittable { app.swipeUp() } // the grid makes cards as they come into view
        XCTAssertTrue(album.exists, "no album called \(title)")
        album.tap()
        XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 20), "the smart album didn't open on its items")
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS 'on the web'")).firstMatch.exists, "still sent to the web")
        Thread.sleep(forTimeInterval: 8) // its pictures
        // Every month's, not only the first's (each month's placeholders once shared one set of ids).
        XCTAssertGreaterThanOrEqual(app.descendants(matching: .any).matching(identifier: "tile").count, 4, "only the first months' items came")
        shot(app, "a smart album")
        app.swipeUp()
        Thread.sleep(forTimeInterval: 4)
        shot(app, "a smart album, further down")
    }

    /// A shared album's activity (comments, who added what) and picks, saying something and taking it
    /// back; in the viewer, Pick (and unpick) and an item's comments.
    func testSharedAlbumTalk() throws {
        guard let title = env["TALK_ALBUM"], !title.isEmpty else { throw XCTSkip("TALK_ALBUM isn't set") }
        let app = launch()
        tab(app, "Collections")
        let album = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'album' AND label BEGINSWITH %@", title + ",")).firstMatch
        _ = element(app, "album").waitForExistence(timeout: 30)
        if app.buttons["All albums"].exists { app.buttons["All albums"].tap() } // with a few albums, they're on Collections itself
        for _ in 0..<8 where !album.isHittable { app.swipeUp() }
        XCTAssertTrue(album.exists, "no album called \(title)")
        album.tap()
        XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 20), "the album didn't open")

        // Its activity: what's been said.
        let activity = element(app, "album-activity")
        XCTAssertTrue(activity.waitForExistence(timeout: 10), "no Activity for a shared album")
        activity.tap()
        XCTAssertTrue(element(app, "talk-entry").waitForExistence(timeout: 15), "no activity")
        Thread.sleep(forTimeInterval: 2) // thumbnails
        shot(app, "a shared album's activity")

        // Saying something, then taking it back.
        let words = "Testing, testing \(Int.random(in: 100...999))"
        let input = element(app, "talk-input")
        input.tap()
        input.typeText(words)
        element(app, "talk-send").tap()
        let mine = app.descendants(matching: .any).matching(NSPredicate(format: "identifier == 'talk-entry' AND label CONTAINS %@", words)).firstMatch
        XCTAssertTrue(mine.waitForExistence(timeout: 15), "what was said didn't show")
        shot(app, "said something")
        mine.swipeLeft()
        app.buttons["Delete"].firstMatch.tap()
        XCTAssertTrue(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: mine)], timeout: 10) == .completed, "it wasn't taken back")

        // Its picks, and one of them opened (the sheet goes, the viewer comes).
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Picks'")).firstMatch.tap()
        XCTAssertTrue(element(app, "talk-pick").waitForExistence(timeout: 10), "no picks")
        Thread.sleep(forTimeInterval: 2)
        shot(app, "a shared album's picks")
        element(app, "talk-pick").tap()

        // In the viewer: Pick, and back; the item's comments.
        let pick = element(app, "pick")
        XCTAssertTrue(pick.waitForExistence(timeout: 10), "a pick didn't open in the viewer, with Pick")
        let was = pick.value as? String ?? ""
        pick.tap()
        XCTAssertTrue(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "value != %@", was), object: pick)], timeout: 5) == .completed, "Pick didn't change")
        shot(app, "picked in the viewer")
        pick.tap()
        XCTAssertTrue(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", was), object: pick)], timeout: 5) == .completed, "Pick didn't go back")
        // Saying something about the item (the keyboard over the viewer), its count, and taking it back.
        let comments = element(app, "comments")
        let before = comments.label
        comments.tap()
        XCTAssertTrue(app.navigationBars["Comments"].waitForExistence(timeout: 10), "no comments sheet")
        let about = "About this one \(Int.random(in: 100...999))"
        let box = element(app, "talk-input")
        box.tap()
        box.typeText(about)
        shot(app, "commenting in the viewer")
        element(app, "talk-send").tap()
        let said = app.descendants(matching: .any).matching(NSPredicate(format: "identifier == 'item-comment' AND label CONTAINS %@", about)).firstMatch
        XCTAssertTrue(said.waitForExistence(timeout: 15), "the item's comment didn't show")
        shot(app, "an item's comments")
        app.buttons["Done"].tap()
        XCTAssertTrue(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "label != %@", before), object: comments)], timeout: 5) == .completed, "the count didn't change")
        comments.tap()
        XCTAssertTrue(said.waitForExistence(timeout: 15))
        said.swipeLeft()
        app.buttons["Delete"].firstMatch.tap()
        XCTAssertTrue(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: said)], timeout: 10) == .completed, "the item's comment wasn't taken back")
        app.buttons["Done"].tap()
        XCTAssertTrue(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "label == %@", before), object: comments)], timeout: 5) == .completed, "the count didn't go back")
    }

    /// No albums: New album asks for a title. Then one from chosen items (what the empty state
    /// suggests), from a place's items.
    func testFirstAlbum() throws {
        let app = launch()
        tab(app, "Collections")
        let first = element(app, "first-album")
        XCTAssertTrue(first.waitForExistence(timeout: 20), "no empty state for albums")
        first.tap()
        XCTAssertTrue(app.alerts["New album"].waitForExistence(timeout: 5), "New album didn't ask for a title")
        shot(app, "new album from the empty state")
        app.alerts["New album"].buttons["Cancel"].tap()

        tab(app, "Search")
        let place = element(app, "suggest-place")
        XCTAssertTrue(place.waitForExistence(timeout: 30))
        place.tap()
        XCTAssertTrue(element(app, "tile").waitForExistence(timeout: 20))
        // The page's own Select: the Library tab's is in the hierarchy too, out of sight.
        let select = app.buttons.matching(NSPredicate(format: "label == 'Select'")).allElementsBoundByIndex.first { $0.isHittable }
        XCTAssertNotNil(select, "no Select on the place's page")
        select?.tap()
        XCTAssertTrue(tabBar(app, shown: false), "the tab bar stayed while choosing on a pushed page")
        let tiles = app.descendants(matching: .any).matching(identifier: "tile")
        tiles.element(boundBy: 0).tap()
        tiles.element(boundBy: 1).tap()
        element(app, "chosen-Add to album").tap()
        let title = app.textFields["New album"]
        XCTAssertTrue(title.waitForExistence(timeout: 5))
        title.tap()
        title.typeText("Redmond walk")
        app.buttons["Make"].tap()
        XCTAssertTrue(tabBar(app, shown: true), "the tab bar didn't come back on the place's page when choosing ended")
        shot(app, "a place's items, the album made")

        tab(app, "Collections")
        XCTAssertTrue(element(app, "album").waitForExistence(timeout: 30), "the new album didn't show")
        Thread.sleep(forTimeInterval: 3)
        shot(app, "collections with an album")
    }
}
