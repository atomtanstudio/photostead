@testable import Photostead
import XCTest

/// When the backup notices show: "backed up" at most once an hour (a later one takes in what came
/// meanwhile), and "can't reach Photostead" a day after it first couldn't, while items wait.
final class NoticeTests: XCTestCase {
    private let t0 = Date(timeIntervalSince1970: 1_800_000_000)
    private func at(_ minutes: Double) -> Date { t0.addingTimeInterval(minutes * 60) }

    private func arrive(_ clock: inout NoticeClock, photos: Int = 0, videos: Int = 0, at now: Date) {
        for _ in 0..<photos { clock.arrived(video: false, now: now) }
        for _ in 0..<videos { clock.arrived(video: true, now: now) }
    }

    func testTheFirstShowsAtOnce() throws {
        var clock = NoticeClock()
        arrive(&clock, photos: 2, videos: 1, at: t0)
        let notice = try XCTUnwrap(clock.finished(now: t0))
        XCTAssertEqual(notice.count, NoticeClock.Count(photos: 2, videos: 1))
        XCTAssertEqual(notice.at, t0)
    }

    func testNothingSentNothingSaid() {
        var clock = NoticeClock()
        XCTAssertNil(clock.finished(now: t0))
        arrive(&clock, photos: 1, at: t0)
        XCTAssertNotNil(clock.finished(now: t0))
        XCTAssertNil(clock.finished(now: at(5)), "said twice")
        XCTAssertNil(clock.finished(now: at(120)), "said again after the hour")
    }

    func testAtMostOneAnHour() throws {
        var clock = NoticeClock()
        arrive(&clock, photos: 5, at: t0)
        _ = clock.finished(now: t0)
        arrive(&clock, photos: 3, at: at(10))
        let later = try XCTUnwrap(clock.finished(now: at(10)))
        XCTAssertEqual(later.at, at(60), "not an hour after the last")
        XCTAssertEqual(later.count.total, 3, "told again what the first said")
        // More before it shows: the same notice, at the same time, with them in.
        arrive(&clock, videos: 2, at: at(30))
        let more = try XCTUnwrap(clock.finished(now: at(30)))
        XCTAssertEqual(more.at, at(60))
        XCTAssertEqual(more.count, NoticeClock.Count(photos: 3, videos: 2))
        // After it showed: the next an hour after that.
        arrive(&clock, photos: 1, at: at(70))
        let next = try XCTUnwrap(clock.finished(now: at(70)))
        XCTAssertEqual(next.at, at(120))
        XCTAssertEqual(next.count, NoticeClock.Count(photos: 1, videos: 0), "counted what the last one said")
    }

    func testAfterTheHourAtOnce() throws {
        var clock = NoticeClock()
        arrive(&clock, photos: 1, at: t0)
        _ = clock.finished(now: t0)
        arrive(&clock, photos: 4, at: at(180))
        let notice = try XCTUnwrap(clock.finished(now: at(180)))
        XCTAssertEqual(notice.at, at(180))
        XCTAssertEqual(notice.count.total, 4)
    }

    func testStillOutWithIOSWaitsForTheRest() throws {
        // The app is told of what's sent as it goes; the notice comes when nothing's left out, so
        // arrivals across several wakes add up into one.
        var clock = NoticeClock()
        arrive(&clock, photos: 4, at: t0)
        arrive(&clock, photos: 6, at: at(3))
        XCTAssertEqual(try XCTUnwrap(clock.finished(now: at(3))).count.total, 10)
    }

    func testOpeningTheAppTakesBackOneToCome() throws {
        var clock = NoticeClock()
        arrive(&clock, photos: 1, at: t0)
        _ = clock.finished(now: t0)
        arrive(&clock, photos: 2, at: at(10))
        _ = clock.finished(now: at(10)) // set for at(60)
        XCTAssertTrue(clock.opened(now: at(20)), "nothing to take back")
        XCTAssertNil(clock.finished(now: at(25)), "told what the app showed")
        XCTAssertFalse(clock.opened(now: at(30)))
        // The hour still runs from the one that showed.
        arrive(&clock, photos: 1, at: at(40))
        XCTAssertEqual(try XCTUnwrap(clock.finished(now: at(40))).at, at(60))
    }

    func testStuckADayAfterItFirstCouldNot() throws {
        var clock = NoticeClock()
        XCTAssertEqual(clock.unreachable(waiting: 5, now: t0), at(24 * 60))
        XCTAssertNil(clock.unreachable(waiting: 5, now: at(30)), "set again (or later) on each try")
        XCTAssertTrue(clock.reached(), "nothing to take back")
        XCTAssertFalse(clock.reached())
        XCTAssertEqual(clock.unreachable(waiting: 1, now: at(100)), at(100 + 24 * 60), "a new stretch out of reach starts again")
    }

    func testStuckOnlyWhileItemsWait() {
        var clock = NoticeClock()
        XCTAssertNil(clock.unreachable(waiting: 0, now: t0), "nothing waiting")
        // A photo taken a day and more later, still out of reach: now.
        XCTAssertEqual(clock.unreachable(waiting: 1, now: at(26 * 60)), at(26 * 60))
        var fresh = NoticeClock()
        XCTAssertFalse(fresh.reached(), "a fresh clock had one")
    }

    func testTitles() {
        XCTAssertEqual(NoticeClock.title(.init(photos: 12, videos: 0)), "12 photos backed up")
        XCTAssertEqual(NoticeClock.title(.init(photos: 1, videos: 0)), "1 photo backed up")
        XCTAssertEqual(NoticeClock.title(.init(photos: 0, videos: 1)), "1 video backed up")
        XCTAssertEqual(NoticeClock.title(.init(photos: 0, videos: 4)), "4 videos backed up")
        XCTAssertEqual(NoticeClock.title(.init(photos: 3, videos: 1)), "3 photos and 1 video backed up")
        XCTAssertEqual(NoticeClock.title(.init(photos: 1, videos: 2)), "1 photo and 2 videos backed up")
    }

    func testKeptBetweenLaunches() throws {
        var clock = NoticeClock()
        arrive(&clock, photos: 2, videos: 1, at: t0)
        _ = clock.finished(now: t0)
        _ = clock.unreachable(waiting: 3, now: t0)
        let again = try JSONDecoder().decode(NoticeClock.self, from: JSONEncoder().encode(clock))
        XCTAssertEqual(again, clock)
    }
}
