// Pairing and a first backup, against a test server: the pairing link (as the Camera app opens it
// from the QR code), confirmed, or the address and code typed in; then everything in the
// simulator's Photos library goes up. Needs PAIR_LINK or PAIR_CODE with PAIR_ADDRESS (pass them as
// TEST_RUNNER_PAIR_… to xcodebuild), and Photos access granted (xcrun simctl privacy).

import XCTest

final class BackupFlowTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    private func launch() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-reset"]
        app.launch()
        return app
    }

    private func showBackup(_ app: XCUIApplication) {
        let pill = app.buttons["backup-pill"]
        if pill.waitForExistence(timeout: 20) { pill.tap() }
    }

    private func shot(_ app: XCUIApplication, _ name: String) {
        let s = XCTAttachment(screenshot: app.screenshot())
        s.name = name
        s.lifetime = .keepAlways
        add(s)
    }

    private func waitForBackup(_ app: XCUIApplication) {
        showBackup(app)
        let status = app.staticTexts["status"]
        XCTAssertTrue(status.waitForExistence(timeout: 20), "the backup screen didn't appear")
        let upToDate = NSPredicate(format: "label == %@", "Up to date")
        expectation(for: upToDate, evaluatedWith: status)
        waitForExpectations(timeout: 120)
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.lifetime = .keepAlways
        add(shot)
    }

    func testPairByLinkAndBackUp() throws {
        guard let link = env["PAIR_LINK"], let url = URL(string: link) else { throw XCTSkip("needs PAIR_LINK") }
        let app = launch()
        XCUIDevice.shared.system.open(url)
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let open = springboard.buttons["Open"]
        if open.waitForExistence(timeout: 5) { open.tap() }
        let confirm = app.buttons["confirm-pair"]
        XCTAssertTrue(confirm.waitForExistence(timeout: 10), "the link wasn't shown to confirm")
        let sheet = XCTAttachment(screenshot: app.screenshot())
        sheet.lifetime = .keepAlways
        add(sheet)
        confirm.tap()
        passFirstRun(app)
        waitForBackup(app)
    }

    private func pairByTyping(_ app: XCUIApplication, code: String, address: String) {
        typeInstead(app)
        let field = app.textFields["address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        field.typeText(address)
        let codeField = app.textFields["code"]
        codeField.tap()
        codeField.typeText(code)
        app.buttons["pair"].tap()
        passFirstRun(app)
    }

    /// The pairing screen leads with scanning: the address and code fields are a tap away.
    private func typeInstead(_ app: XCUIApplication) {
        let instead = app.buttons["type-instead"]
        if instead.waitForExistence(timeout: 10) { instead.tap() }
    }

    /// After pairing, the first run (once per phone): through it, allowing Photos if iOS asks.
    private func passFirstRun(_ app: XCUIApplication) {
        let done = app.buttons["welcome-done"], go = app.buttons["welcome-continue"]
        guard done.waitForExistence(timeout: 20) || go.exists else { return }
        if go.exists {
            go.tap()
            let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Allow Full Access"]
            if allow.waitForExistence(timeout: 5) { allow.tap() }
            XCTAssertTrue(done.waitForExistence(timeout: 10))
        }
        done.tap()
    }

    /// Closed while sending: iOS carries on, and reopening it notes what arrived meanwhile.
    func testCarriesOnWhenClosed() throws {
        // A slow way to the server (PAIR_ADDRESS_CLOSED, a throttling relay) gives it time to close mid-send.
        guard let code = env["PAIR_CODE_CLOSED"], let address = env["PAIR_ADDRESS_CLOSED"] ?? env["PAIR_ADDRESS"] else {
            throw XCTSkip("needs PAIR_CODE_CLOSED and PAIR_ADDRESS (or PAIR_ADDRESS_CLOSED)")
        }
        let app = launch()
        pairByTyping(app, code: code, address: address)
        showBackup(app)
        let status = app.staticTexts["status"]
        XCTAssertTrue(status.waitForExistence(timeout: 20))
        expectation(for: NSPredicate(format: "label BEGINSWITH %@", "Sending"), evaluatedWith: status)
        waitForExpectations(timeout: 180)
        let sending = XCTAttachment(screenshot: app.screenshot())
        sending.lifetime = .keepAlways
        add(sending)
        print("PHOTOSTEAD-CLOSED-AT \(Date().timeIntervalSince1970)")
        app.terminate()
        Thread.sleep(forTimeInterval: 60)
        print("PHOTOSTEAD-REOPENED-AT \(Date().timeIntervalSince1970)")
        let again = XCUIApplication()
        again.launch()
        waitForBackup(again)
    }

    /// The library from the sync feed: the grid by month, an item in the viewer, and albums.
    func testBrowse() throws {
        guard let code = env["PAIR_CODE_BROWSE"], let address = env["PAIR_ADDRESS"] else { throw XCTSkip("needs PAIR_CODE_BROWSE and PAIR_ADDRESS") }
        let app = launch()
        pairByTyping(app, code: code, address: address)
        let tile = app.descendants(matching: .any).matching(identifier: "tile").firstMatch
        XCTAssertTrue(tile.waitForExistence(timeout: 60), "the library didn't show")
        Thread.sleep(forTimeInterval: 3) // thumbnails
        shot(app, "library")
        tile.tap()
        let close = app.buttons["close"]
        XCTAssertTrue(close.waitForExistence(timeout: 10), "the viewer didn't open")
        Thread.sleep(forTimeInterval: 2)
        shot(app, "viewer")
        app.swipeLeft()
        Thread.sleep(forTimeInterval: 2)
        shot(app, "viewer, next")
        close.tap()
        app.tabBars.buttons["Collections"].tap()
        let album = app.descendants(matching: .any).matching(identifier: "album").firstMatch
        XCTAssertTrue(album.waitForExistence(timeout: 10), "no albums")
        Thread.sleep(forTimeInterval: 2)
        shot(app, "albums")
        album.tap()
        Thread.sleep(forTimeInterval: 2)
        shot(app, "an album")
    }

    /// A library the size of a real one (94,000 items, made up): it shows quickly, the scrubber goes
    /// years back, pinching changes the grid, tapping the tab goes back to the top, and the viewer
    /// zooms, shows details on a swipe up, and goes away on a swipe down.
    /// Wider windows (turned sideways; an iPhone Duo opened): more tiles across at the same size,
    /// and the viewer's bar where iOS puts toolbars. No server.
    func testWideScreens() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-reset", "-demoLibrary", "3000"]
        app.launch()
        let tile = app.descendants(matching: .any).matching(identifier: "tile").element(boundBy: 2)
        XCTAssertTrue(tile.waitForExistence(timeout: 60), "the library didn't show")
        let upright = tile.frame.width
        print("PHOTOSTEAD-WIDE window \(app.windows.firstMatch.frame) tile \(upright)")
        shot(app, "library")

        XCUIDevice.shared.orientation = .landscapeLeft
        Thread.sleep(forTimeInterval: 2)
        let sideways = app.descendants(matching: .any).matching(identifier: "tile").element(boundBy: 2)
        XCTAssertTrue(sideways.waitForExistence(timeout: 10))
        print("PHOTOSTEAD-WIDE turned window \(app.windows.firstMatch.frame) tile \(sideways.frame.width)")
        XCTAssertEqual(sideways.frame.width, upright, accuracy: upright * 0.25, "tiles changed size when turned")
        shot(app, "library turned")
        if let pause = env["PAUSE_TURNED"], let s = Double(pause) { Thread.sleep(forTimeInterval: s) }

        sideways.tap()
        Thread.sleep(forTimeInterval: 2)
        shot(app, "viewer turned, first")
        print("PHOTOSTEAD-WIDE buttons \(app.buttons.allElementsBoundByIndex.map { "\($0.identifier)/\($0.label)@\($0.frame)" })")
        XCTAssertTrue(app.buttons["Details"].waitForExistence(timeout: 10), "no viewer bar turned")
        print("PHOTOSTEAD-WIDE bar \(app.buttons["Share"].frame) \(app.buttons["Move to the trash"].frame)")
        shot(app, "viewer turned")
        if let pause = env["PAUSE_TURNED"], let s = Double(pause) { Thread.sleep(forTimeInterval: s) } // for a screenshot from outside

        XCUIDevice.shared.orientation = .portrait
        Thread.sleep(forTimeInterval: 2)
        XCTAssertTrue(app.buttons["Details"].waitForExistence(timeout: 10), "no viewer bar upright")
        print("PHOTOSTEAD-WIDE bar upright \(app.buttons["Share"].frame) \(app.buttons["Move to the trash"].frame)")
        shot(app, "viewer upright")
        app.buttons["close"].tap()

        app.buttons["Collections"].firstMatch.tap() // a tab bar down the side on an open iPhone Duo isn't a TabBar
        Thread.sleep(forTimeInterval: 1)
        shot(app, "collections")
    }

    func testLibraryAtSizeAndGestures() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-reset", "-demoLibrary", "94000"]
        let started = Date()
        app.launch()
        let tile = app.descendants(matching: .any).matching(identifier: "tile").firstMatch
        XCTAssertTrue(tile.waitForExistence(timeout: 90), "the library didn't show")
        print("PHOTOSTEAD-DEMO shown after \(String(format: "%.1f", Date().timeIntervalSince(started)))s")
        shot(app, "library at size")

        // The scrubber: down to about two thirds, years back.
        let scrubber = app.descendants(matching: .any).matching(identifier: "scrubber").firstMatch
        XCTAssertTrue(scrubber.waitForExistence(timeout: 5))
        let from = scrubber.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.05))
        let to = scrubber.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.66))
        from.press(forDuration: 0.2, thenDragTo: to, withVelocity: .slow, thenHoldForDuration: 0.6)
        Thread.sleep(forTimeInterval: 1)
        shot(app, "scrubbed")

        // Scrolling shows the scrubber's handle.
        app.scrollViews.firstMatch.swipeUp(velocity: .slow)
        shot(app, "scrolling")

        // Pinch in: more across.
        app.scrollViews.firstMatch.pinch(withScale: 0.5, velocity: -1)
        Thread.sleep(forTimeInterval: 1)
        shot(app, "pinched")

        // Back to the top by tapping the tab again.
        app.tabBars.buttons["Library"].tap()
        Thread.sleep(forTimeInterval: 1.5)
        shot(app, "back to the top")

        // The viewer: zoom, details, and away.
        app.descendants(matching: .any).matching(identifier: "tile").element(boundBy: 1).tap() // a photo (the first is a video)
        let close = app.buttons["close"]
        XCTAssertTrue(close.waitForExistence(timeout: 10), "the viewer didn't open")
        let shown = app.descendants(matching: .any).matching(identifier: "shown").firstMatch
        shown.doubleTap()
        Thread.sleep(forTimeInterval: 1)
        shot(app, "zoomed")
        shown.doubleTap()
        Thread.sleep(forTimeInterval: 1)
        shown.swipeUp()
        XCTAssertTrue(app.staticTexts["Details"].waitForExistence(timeout: 5), "no details on a swipe up")
        shot(app, "details")
        app.buttons["Done"].tap() // the sheet
        Thread.sleep(forTimeInterval: 1)
        shown.swipeDown(velocity: .fast) // the viewer
        Thread.sleep(forTimeInterval: 1.5)
        XCTAssertFalse(close.exists, "a swipe down didn't put it away")
        shot(app, "put away")
    }

    /// Step 1 of the plan: favorite, add to a new album, details, the trash and back, search, settings.
    /// Downloading at full size from the viewer: into Photos, and the Files picker for a copy.
    func testDownload() throws {
        guard let code = env["PAIR_CODE_DOWNLOAD"], let address = env["PAIR_ADDRESS"] else { throw XCTSkip("needs PAIR_CODE_DOWNLOAD and PAIR_ADDRESS") }
        let app = launch()
        pairByTyping(app, code: code, address: address)
        let tile = app.descendants(matching: .any).matching(identifier: "tile").element(boundBy: 0)
        XCTAssertTrue(tile.waitForExistence(timeout: 60), "the library didn't show")
        tile.tap()
        let download = app.buttons["Download"]
        XCTAssertTrue(download.waitForExistence(timeout: 10), "no Download in the viewer")
        download.tap()
        XCTAssertTrue(app.buttons["Save to Photos"].waitForExistence(timeout: 5))
        shot(app, "download menu")
        app.buttons["Save to Photos"].tap()
        // The first time, iOS asks whether Photostead may add to Photos.
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let allow = springboard.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Allow'")).firstMatch
        let saved = app.staticTexts["Saved to Photos"]
        let failed = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Couldn'")).firstMatch
        var outcome = ""
        for _ in 0..<120 where outcome.isEmpty { // its note shows for a moment: look often
            if allow.exists { allow.tap() }
            if saved.exists { outcome = "saved" } else if failed.exists { outcome = failed.label }
            Thread.sleep(forTimeInterval: 0.25)
        }
        XCTAssertEqual(outcome, "saved", "Save to Photos")

        download.tap()
        app.buttons["Save to Files"].tap()
        XCTAssertTrue(app.buttons["Save"].waitForExistence(timeout: 30), "no Files picker")
        shot(app, "save to files")
        app.buttons["Save"].tap() // (a copy left in On My iPhone by an earlier run asks to replace it, out of reach here)
        let inFiles = app.staticTexts["Saved to Files"]
        var found = false
        for _ in 0..<60 where !found {
            found = inFiles.exists
            Thread.sleep(forTimeInterval: 0.25)
        }
        if !found { shot(app, "after Save to Files") }
        XCTAssertTrue(found, "not saved to Files")
    }

    func testFindAndShare() throws {
        guard let code = env["PAIR_CODE_FIND"], let address = env["PAIR_ADDRESS"] else { throw XCTSkip("needs PAIR_CODE_FIND and PAIR_ADDRESS") }
        let app = launch()
        pairByTyping(app, code: code, address: address)
        let tile = app.descendants(matching: .any).matching(identifier: "tile").element(boundBy: 1)
        XCTAssertTrue(tile.waitForExistence(timeout: 60), "the library didn't show")
        XCTAssertTrue(app.buttons["backup-pill"].exists, "no backup pill")
        shot(app, "library with the pill")
        tile.tap()

        // Favorite (from not, whatever an earlier run left).
        let favorite = app.buttons["Favorite"]
        XCTAssertTrue(app.buttons["Details"].waitForExistence(timeout: 10), "no viewer bar")
        if app.buttons["Unfavorite"].exists { app.buttons["Unfavorite"].tap() }
        XCTAssertTrue(favorite.waitForExistence(timeout: 10))
        shot(app, "viewer bar")
        favorite.tap()
        XCTAssertTrue(app.buttons["Unfavorite"].waitForExistence(timeout: 10), "favorite didn't take")

        // Into a new album.
        app.buttons["Add to album"].tap()
        let name = app.textFields["New album"]
        XCTAssertTrue(name.waitForExistence(timeout: 10))
        name.tap()
        name.typeText("From the phone\n")
        XCTAssertTrue(app.staticTexts["Added to From the phone"].waitForExistence(timeout: 15), "not added")

        // Details from the server.
        app.buttons["Details"].tap()
        XCTAssertTrue(app.staticTexts["File"].waitForExistence(timeout: 10), "no details from the server")
        shot(app, "details")
        app.buttons["Done"].tap()

        // To the trash.
        app.buttons["Move to the trash"].tap()
        let confirm = app.buttons["Move to the trash"].firstMatch
        let sheetButton = app.sheets.buttons["Move to the trash"].exists ? app.sheets.buttons["Move to the trash"] : app.scrollViews.buttons["Move to the trash"]
        (sheetButton.exists ? sheetButton : confirm).tap()
        XCTAssertTrue(app.staticTexts["Moved to the trash"].waitForExistence(timeout: 15), "not trashed")
        app.buttons["close"].tap()

        // Collections: the new album, and the trash, from which it comes back.
        app.tabBars.buttons["Collections"].tap()
        XCTAssertTrue(app.staticTexts["From the phone"].waitForExistence(timeout: 15), "the new album isn't in Collections")
        shot(app, "collections")
        app.swipeUp() // the trash sits under the tab bar until scrolled to
        app.buttons["collection-Trash"].tap()
        let trashed = app.descendants(matching: .any).matching(identifier: "tile").firstMatch
        XCTAssertTrue(trashed.waitForExistence(timeout: 15), "the trash is empty")
        shot(app, "trash")
        trashed.tap()
        let restoreBar = app.buttons["Restore"].firstMatch
        XCTAssertTrue(restoreBar.waitForExistence(timeout: 10), "no Restore in the trash's viewer")
        restoreBar.tap()
        Thread.sleep(forTimeInterval: 1)
        let restore = app.sheets.buttons["Restore"].exists ? app.sheets.buttons["Restore"] : app.scrollViews.buttons["Restore"]
        if restore.exists { restore.tap() }
        // The only one there: the viewer closes on an empty trash.
        XCTAssertTrue(app.staticTexts["The trash is empty."].waitForExistence(timeout: 15), "not restored")
        shot(app, "trash after restoring")

        // Search.
        app.tabBars.buttons["Search"].tap()
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        shot(app, "search before typing")
        field.tap()
        field.typeText("photo\n")
        XCTAssertTrue(app.staticTexts["found"].waitForExistence(timeout: 20), "no search results")
        shot(app, "search results")

        // Settings behind your picture (searching hides the toolbar, as iOS does).
        app.tabBars.buttons["Library"].tap()
        app.buttons["account"].firstMatch.tap()
        XCTAssertTrue(app.switches["backup-on"].waitForExistence(timeout: 10), "no settings")
        shot(app, "settings")
    }

    /// Step 2: choosing several (Select, press and hold), and doing things to them: favorites, the
    /// archive and back, the vault (a PIN set the first time) and out again; the filter.
    func testChooseAndOrganize() throws {
        guard let code = env["PAIR_CODE_ORGANIZE"], let address = env["PAIR_ADDRESS"] else { throw XCTSkip("needs PAIR_CODE_ORGANIZE and PAIR_ADDRESS") }
        let app = launch()
        pairByTyping(app, code: code, address: address)
        let tiles = app.descendants(matching: .any).matching(identifier: "tile")
        XCTAssertTrue(tiles.element(boundBy: 3).waitForExistence(timeout: 60), "the library didn't show")

        // Select, two tiles, favorites.
        app.buttons["select"].tap()
        tiles.element(boundBy: 1).tap()
        tiles.element(boundBy: 2).tap()
        XCTAssertTrue(app.staticTexts["2 chosen"].waitForExistence(timeout: 5), "not 2 chosen")
        shot(app, "two chosen")
        (app.buttons["chosen-Favorite"].exists ? app.buttons["chosen-Favorite"] : app.buttons["chosen-Unfavorite"]).tap() // as earlier runs left them
        XCTAssertTrue(app.buttons["select"].waitForExistence(timeout: 15), "choosing didn't end")

        // Press and hold: archive it, find it in the archive, bring it back.
        tiles.element(boundBy: 1).press(forDuration: 0.8)
        XCTAssertTrue(app.staticTexts["1 chosen"].waitForExistence(timeout: 5), "press and hold didn't choose")
        app.buttons["chosen-more"].tap()
        shot(app, "more")
        app.buttons["Archive"].tap()
        XCTAssertTrue(app.buttons["select"].waitForExistence(timeout: 15))
        app.tabBars.buttons["Collections"].tap()
        app.swipeUp()
        app.buttons["collection-Archive"].tap()
        XCTAssertTrue(tiles.firstMatch.waitForExistence(timeout: 15), "the archive is empty")
        shot(app, "archive")
        app.buttons["Select"].tap()
        tiles.firstMatch.tap()
        app.buttons["chosen-more"].tap()
        app.buttons["Bring back from the archive"].tap()
        XCTAssertTrue(app.staticTexts["Nothing here."].waitForExistence(timeout: 15), "not back from the archive")
        app.navigationBars.buttons.element(boundBy: 0).tap()

        // Into the vault, and out of it.
        app.tabBars.buttons["Library"].tap()
        XCTAssertTrue(tiles.element(boundBy: 1).waitForExistence(timeout: 10))
        tiles.element(boundBy: 1).press(forDuration: 0.8)
        app.buttons["chosen-more"].tap()
        app.buttons["Move to the vault"].tap()
        let pin = app.secureTextFields["vault-pin"]
        if pin.waitForExistence(timeout: 5) { // the vault's first time: a PIN set first
            shot(app, "vault PIN first")
            pin.tap()
            pin.typeText("2468")
            app.secureTextFields["vault-pin-again"].tap()
            app.secureTextFields["vault-pin-again"].typeText("2468")
            app.buttons["vault-set"].tap()
        } else {
            let confirm = app.sheets.buttons["Move to the vault"].exists ? app.sheets.buttons["Move to the vault"] : app.scrollViews.buttons["Move to the vault"]
            XCTAssertTrue(confirm.waitForExistence(timeout: 5))
            confirm.tap()
        }
        XCTAssertTrue(app.buttons["select"].waitForExistence(timeout: 15), "not moved to the vault")
        app.tabBars.buttons["Collections"].tap()
        app.swipeUp()
        app.buttons["collection-Vault"].tap()
        // Locked (it asks for the PIN), or open still from setting it just now.
        let opened = tiles.firstMatch
        _ = pin.waitForExistence(timeout: 10) || opened.waitForExistence(timeout: 5)
        if pin.exists {
            shot(app, "vault locked")
            pin.tap()
            pin.typeText("2468")
            app.buttons["vault-open"].tap()
        }
        let notNow = app.buttons["Not now"]
        if notNow.waitForExistence(timeout: 5) { notNow.tap() }
        XCTAssertTrue(tiles.firstMatch.waitForExistence(timeout: 15), "the vault is empty")
        shot(app, "vault")
        app.buttons["Select"].tap()
        for i in 0..<tiles.count { tiles.element(boundBy: i).tap() } // all of it (earlier runs may have left some)
        app.buttons["chosen-out-of-vault"].tap()
        XCTAssertTrue(app.staticTexts["Nothing here."].waitForExistence(timeout: 15), "not out of the vault")
        app.navigationBars.buttons.element(boundBy: 0).tap()

        // The filter: videos only, and the pill that clears it.
        app.tabBars.buttons["Library"].tap()
        app.buttons["filter"].tap()
        app.buttons["Videos"].tap()
        XCTAssertTrue(app.buttons["filter-pill"].waitForExistence(timeout: 5), "no filter pill")
        shot(app, "videos only")
        app.buttons["filter-pill"].tap()
        XCTAssertFalse(app.buttons["filter-pill"].waitForExistence(timeout: 2))
    }

    /// Step 3: sharing an album from the phone: a link (and its QR code), and someone new by name.
    func testShareAlbum() throws {
        guard let code = env["PAIR_CODE_SHARE"], let address = env["PAIR_ADDRESS"] else { throw XCTSkip("needs PAIR_CODE_SHARE and PAIR_ADDRESS") }
        let app = launch()
        pairByTyping(app, code: code, address: address)
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "tile").firstMatch.waitForExistence(timeout: 60))
        app.tabBars.buttons["Collections"].tap()
        let all = app.buttons["All albums"]
        if all.waitForExistence(timeout: 10) { all.tap() }
        let beach = app.staticTexts["Beach day"]
        XCTAssertTrue(beach.waitForExistence(timeout: 10), "no Beach day album")
        beach.tap()
        let share = app.buttons["share-album"]
        XCTAssertTrue(share.waitForExistence(timeout: 10), "no Share on the album")
        share.tap()
        let make = app.buttons["make-link"]
        XCTAssertTrue(make.waitForExistence(timeout: 15), "no links section")
        let before = app.buttons.matching(identifier: "QR code").count
        make.tap()
        let qr = app.buttons.matching(identifier: "QR code")
        let deadline = Date().addingTimeInterval(15)
        while qr.count <= before && Date() < deadline { Thread.sleep(forTimeInterval: 0.5) }
        XCTAssertGreaterThan(qr.count, before, "no new link")
        shot(app, "album sharing")
        qr.element(boundBy: qr.count - 1).tap()
        XCTAssertTrue(app.staticTexts["Scan to see the photos"].waitForExistence(timeout: 10), "no QR card")
        shot(app, "qr card")
        app.buttons["Done"].firstMatch.tap()

        app.buttons["share-with-someone"].tap()
        let name = app.textFields["Their name"]
        XCTAssertTrue(name.waitForExistence(timeout: 10))
        name.typeText("Night test \(Int(Date().timeIntervalSince1970) % 100000)")
        app.alerts.buttons["Share"].tap()
        // Their link, to send: the iPhone's share sheet.
        let sheet = app.otherElements["ActivityListView"].firstMatch
        XCTAssertTrue(sheet.waitForExistence(timeout: 15) || app.navigationBars["UIActivityContentView"].exists, "no share sheet with their link")
        shot(app, "send their link")
    }

    /// Dragging sideways across a row chooses it (no server).
    func testDragToChoose() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-reset", "-demoLibrary", "3000"]
        app.launch()
        let tiles = app.descendants(matching: .any).matching(identifier: "tile")
        XCTAssertTrue(tiles.element(boundBy: 8).waitForExistence(timeout: 60), "the library didn't show")
        app.buttons["select"].tap()
        let first = tiles.element(boundBy: 0).frame, last = tiles.element(boundBy: 3).frame
        let window = app.windows.firstMatch
        let from = window.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: first.midX, dy: first.midY))
        let to = window.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: last.midX, dy: last.midY))
        from.press(forDuration: 0.05, thenDragTo: to, withVelocity: .slow, thenHoldForDuration: 0.3)
        let count = app.staticTexts.matching(identifier: "chosen-count").firstMatch
        XCTAssertTrue(count.waitForExistence(timeout: 5))
        print("PHOTOSTEAD-DRAG \(count.label) across \(first) to \(last)")
        XCTAssertNotEqual(count.label, "Choose items", "the drag chose nothing")
        XCTAssertNotEqual(count.label, "1 chosen", "the drag chose only one")
        shot(app, "dragged across")
    }

    func testPairByTyping() throws {
        guard let code = env["PAIR_CODE"], let address = env["PAIR_ADDRESS"] else { throw XCTSkip("needs PAIR_CODE and PAIR_ADDRESS") }
        let app = launch()
        typeInstead(app)
        let field = app.textFields["address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        field.typeText(address)
        let codeField = app.textFields["code"]
        codeField.tap()
        codeField.typeText(code)
        app.buttons["pair"].tap()
        passFirstRun(app)
        waitForBackup(app)
    }

    /// The first screens, for looking at: pairing (and typing it instead), then the first run after
    /// pairing (with the demo library: no server), in light mode, dark mode and the largest text.
    /// Photos access is reset first so the first run asks for it, and allowed at the end.
    func testFirstRunScreens() throws {
        let looks: [(name: String, dark: Bool, args: [String])] = [
            ("light", false, []),
            ("dark", true, []),
            ("largest text", false, ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXL"]),
        ]
        defer { XCUIDevice.shared.appearance = .light }
        for look in looks {
            let app = XCUIApplication()
            app.resetAuthorizationStatus(for: .photos)
            func start(_ args: [String]) {
                app.launchArguments = args + look.args
                app.launch()
                XCUIDevice.shared.appearance = look.dark ? .dark : .light // (set before launching, it didn't take)
                Thread.sleep(forTimeInterval: 1)
            }
            start(["-reset"])
            XCTAssertTrue(app.buttons["scan"].waitForExistence(timeout: 10), "no Scan on the pairing screen")
            shot(app, "1 pairing, \(look.name)")
            if !look.args.isEmpty {
                app.swipeUp()
                shot(app, "1 pairing, \(look.name), lower")
                app.swipeDown()
            }
            app.buttons["type-instead"].tap()
            XCTAssertTrue(app.textFields["address"].waitForExistence(timeout: 5), "no address field")
            app.swipeUp()
            shot(app, "2 pairing typed, \(look.name)")
            if !look.args.isEmpty {
                app.swipeUp()
                shot(app, "2 pairing typed, \(look.name), lower")
            }
            app.terminate()

            start(["-reset", "-demoLibrary", "60", "-welcome"])
            let go = app.buttons["welcome-continue"]
            XCTAssertTrue(go.waitForExistence(timeout: 10), "no first run")
            shot(app, "3 first run photos, \(look.name)")
            go.tap()
            let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Allow Full Access"]
            XCTAssertTrue(allow.waitForExistence(timeout: 10), "iOS didn't ask for Photos")
            shot(app, "4 iOS asks for photos, \(look.name)")
            allow.tap()
            let done = app.buttons["welcome-done"]
            XCTAssertTrue(done.waitForExistence(timeout: 10), "no second page")
            Thread.sleep(forTimeInterval: 1)
            shot(app, "5 first run backup, \(look.name)")
            if !look.args.isEmpty {
                app.swipeUp()
                shot(app, "5 first run backup, \(look.name), lower")
                app.swipeUp()
                shot(app, "5 first run backup, \(look.name), end")
            }
            XCTAssertTrue(app.switches["welcome-notify"].exists, "no notifications switch")
            done.tap()
            XCTAssertTrue(app.buttons["backup-pill"].waitForExistence(timeout: 20), "the library didn't follow")
            shot(app, "6 library after, \(look.name)")
            app.buttons["backup-pill"].tap()
            XCTAssertTrue(app.switches["notify"].waitForExistence(timeout: 10), "no notify switch in Settings")
            app.swipeUp()
            shot(app, "7 settings, \(look.name)")
            app.terminate()
        }
    }

    /// Backup notices, end to end, in two steps with new photos added to the Simulator between
    /// (xcrun simctl addmedia). First (PAIR_CODE_NOTICE): pairs, turns notices on in the first run,
    /// allows them, and backs up what's there.
    func testNoticesOnInTheFirstRun() throws {
        guard let code = env["PAIR_CODE_NOTICE"], let address = env["PAIR_ADDRESS"] else { throw XCTSkip("needs PAIR_CODE_NOTICE and PAIR_ADDRESS") }
        let app = launch()
        typeInstead(app)
        let field = app.textFields["address"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        field.typeText(address)
        app.textFields["code"].tap()
        app.textFields["code"].typeText(code)
        app.buttons["pair"].tap()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let done = app.buttons["welcome-done"], go = app.buttons["welcome-continue"]
        XCTAssertTrue(done.waitForExistence(timeout: 20) || go.exists, "no first run")
        if go.exists {
            go.tap()
            let photos = springboard.buttons["Allow Full Access"]
            if photos.waitForExistence(timeout: 5) { photos.tap() }
        }
        let notify = app.switches["welcome-notify"]
        XCTAssertTrue(notify.waitForExistence(timeout: 10))
        if notify.value as? String != "1" {
            notify.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap() // the switch, not its words
            let allow = springboard.buttons["Allow"]
            if allow.waitForExistence(timeout: 5) { allow.tap() }
        }
        expectation(for: NSPredicate(format: "value == '1'"), evaluatedWith: notify)
        waitForExpectations(timeout: 10)
        shot(app, "notices on in the first run")
        done.tap()
        waitForBackup(app)
    }

    /// Then (NOTICE_AFTER, with new photos added): opened and left at once, so they go up in the
    /// background, and "… backed up" is waiting in Notification Center.
    func testNoticeAfterBackingUpInTheBackground() throws {
        guard env["NOTICE_AFTER"] != nil else { throw XCTSkip("needs NOTICE_AFTER, after testNoticesOnInTheFirstRun and new photos") }
        let app = XCUIApplication()
        app.launch() // still paired, notices on
        Thread.sleep(forTimeInterval: 0.5)
        XCUIDevice.shared.press(.home)
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let notice = springboard.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'backed up'")).firstMatch
        var found = false
        for _ in 0..<12 where !found { // up to 3 minutes: open Notification Center, look, close it
            Thread.sleep(forTimeInterval: 15)
            springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.25, dy: 0.0))
                .press(forDuration: 0.1, thenDragTo: springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.25, dy: 0.6)))
            found = notice.waitForExistence(timeout: 3)
            let s = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
            s.name = found ? "notice" : "no notice yet"
            s.lifetime = .keepAlways
            add(s)
            if !found { XCUIDevice.shared.press(.home) }
        }
        print("PHOTOSTEAD-NOTICE \(found ? notice.label : "none")")
        XCTAssertTrue(found, "no backed-up notice")
    }
}
