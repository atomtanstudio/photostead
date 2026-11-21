// Backup notices: a word on the lock screen when a backup that ran in the background sent something
// new, and when backup hasn't reached Photostead for a day while photos and videos wait. Only when you
// asked for them (in the first run, or Settings → Notify me about backups), and only from the phone
// itself: local notifications, so nothing goes through a push service.
//
// What iOS's own uploads send (Uploads/) isn't counted: the app doesn't hear about those.

import Foundation
import Photos
import UIKit
import UserNotifications

/// When each notice is due, apart from iOS's notification center so it can be tested. Saved in the
/// app's settings: the app may be woken, suspended and closed between one item and the next.
struct NoticeClock: Codable, Equatable {
    struct Count: Codable, Equatable {
        var photos = 0
        var videos = 0
        var total: Int { photos + videos }
    }

    /// Backed up in the background, and in no notice yet.
    var waiting = Count()
    /// In the "backed up" notice set to show at `due`.
    var told = Count()
    var due: Date?
    /// When the last "backed up" notice showed: the next is an hour after it, at the soonest.
    var shown: Date?
    /// Since when backup hasn't reached Photostead (nil: it did, the last time it tried).
    var unreachableSince: Date?
    /// When the notice about it shows (nil: none set, this time it's out of reach).
    var stuckDue: Date?

    static let gap: TimeInterval = 3600
    static let stuckAfter: TimeInterval = 24 * 3600

    /// The notice set for `due` has shown by now: what it said is told.
    mutating func settle(now: Date) {
        guard let due, due <= now else { return }
        shown = due
        self.due = nil
        told = Count()
    }

    /// An item arrived while the app was in the background.
    mutating func arrived(video: Bool, now: Date) {
        settle(now: now)
        if video { waiting.videos += 1 } else { waiting.photos += 1 }
    }

    /// A stretch in the background ended with nothing left out with iOS. Returns the notice to set:
    /// everything sent and not told yet, and when. A notice still to show takes the new ones in;
    /// otherwise it shows now, or an hour after the last one.
    mutating func finished(now: Date) -> (count: Count, at: Date)? {
        settle(now: now)
        guard waiting.total > 0 else { return nil }
        let at = due ?? max(now, shown.map { $0.addingTimeInterval(Self.gap) } ?? now)
        told.photos += waiting.photos
        told.videos += waiting.videos
        waiting = Count()
        due = at
        return (told, at)
    }

    /// The app was opened, and shows how the backup went: a notice still to come isn't needed.
    /// Returns whether there was one, to take back.
    mutating func opened(now: Date) -> Bool {
        settle(now: now)
        let had = due != nil
        waiting = Count()
        told = Count()
        due = nil
        return had
    }

    /// Backup couldn't reach Photostead. With items waiting, a notice is set for a day after it first
    /// couldn't (once each time it's out of reach). Returns when, or nil when there's nothing to set.
    mutating func unreachable(waiting: Int, now: Date) -> Date? {
        let since = unreachableSince ?? now
        unreachableSince = since
        guard waiting > 0, stuckDue == nil else { return nil }
        let at = max(since.addingTimeInterval(Self.stuckAfter), now)
        stuckDue = at
        return at
    }

    /// Photostead answered. Returns whether a notice about not reaching it was set, to take back.
    mutating func reached() -> Bool {
        let had = stuckDue != nil
        unreachableSince = nil
        stuckDue = nil
        return had
    }

    /// The title of a "backed up" notice: "12 photos backed up", "3 photos and 1 video backed up".
    static func title(_ count: Count) -> String {
        func n(_ k: Int, _ one: String, _ many: String) -> String { k == 1 ? "1 \(one)" : "\(k.formatted()) \(many)" }
        switch (count.photos, count.videos) {
        case (_, 0): return "\(n(count.photos, "photo", "photos")) backed up"
        case (0, _): return "\(n(count.videos, "video", "videos")) backed up"
        default: return "\(n(count.photos, "photo", "photos")) and \(n(count.videos, "video", "videos")) backed up"
        }
    }

    private static let key = "noticeClock"

    static func load() -> NoticeClock {
        UserDefaults.standard.data(forKey: key).flatMap { try? JSONDecoder().decode(NoticeClock.self, from: $0) } ?? NoticeClock()
    }

    func save() {
        UserDefaults.standard.set(try? JSONEncoder().encode(self), forKey: Self.key)
    }
}

/// The notices themselves: asks iOS for permission when you turn them on, and sets and takes back
/// what NoticeClock says. Backup tells it what happened (see "Notices" in Backup.swift).
@MainActor
@Observable
final class Notices {
    static let shared = Notices()

    /// You asked for them. Off: none are set, and any waiting are taken back.
    var on: Bool = UserDefaults.standard.bool(forKey: "notifyBackups") {
        didSet {
            UserDefaults.standard.set(on, forKey: "notifyBackups")
            if !on { forget() }
        }
    }
    /// iOS has notifications off for Photostead (Settings → Notifications): on, but they can't show.
    var blocked = false

    @ObservationIgnored private var clock = NoticeClock.load() {
        didSet { clock.save() }
    }
    private var center: UNUserNotificationCenter { .current() }
    private enum ID {
        static let backedUp = "backed-up"
        static let stuck = "stuck"
    }

    private init() {}

    /// Turns them on, asking iOS the first time (you tapped for it, so it's asked in context), or off.
    func turn(_ want: Bool) async {
        guard want else {
            on = false
            blocked = false
            return
        }
        switch await center.notificationSettings().authorizationStatus {
        case .notDetermined:
            // Quiet ones only: no sounds or badges, so none is asked for.
            on = (try? await center.requestAuthorization(options: [.alert])) ?? false
            blocked = false
        case .denied:
            on = true // as you asked, for when they're allowed again
            blocked = true
        default:
            on = true
            blocked = false
        }
    }

    /// Whether iOS lets them show, for Settings to say.
    func check() async {
        let status = await center.notificationSettings().authorizationStatus
        blocked = on && status == .denied
    }

    /// An item finished arriving, so Photostead was reached; counted when the app is in the
    /// background (with it open, you see it go).
    func arrived(_ asset: String) {
        reached()
        guard on, UIApplication.shared.applicationState == .background else { return }
        let video = PHAsset.fetchAssets(withLocalIdentifiers: [asset], options: nil).firstObject?.mediaType == .video
        clock.arrived(video: video, now: Date())
    }

    /// Whether anything sent in the background hasn't been told yet.
    var untold: Bool { on && clock.waiting.total > 0 }

    /// A stretch in the background ended with nothing left out with iOS: "12 photos backed up", now
    /// or an hour after the last one. `left`: what's still to back up.
    func finished(left: Int) async {
        guard on, let (count, at) = clock.finished(now: Date()) else { return }
        let content = UNMutableNotificationContent()
        content.title = NoticeClock.title(count)
        content.body = left == 0 ? "Everything on this iPhone is in Photostead." : "\(left.formatted()) more to go."
        content.interruptionLevel = .passive // news, not a call to do anything: no sound, the screen stays off
        content.threadIdentifier = "backup"
        await add(ID.backedUp, content, at: at)
    }

    /// The app is open: a "backed up" notice still to come is taken back.
    func opened() {
        if clock.opened(now: Date()) { center.removePendingNotificationRequests(withIdentifiers: [ID.backedUp]) }
    }

    /// Backup couldn't reach Photostead, with `waiting` items to back up: a notice for a day after it
    /// first couldn't, unless it's reached again before then.
    func unreachable(waiting: Int) async {
        guard let at = clock.unreachable(waiting: on ? waiting : 0, now: Date()) else { return }
        let content = UNMutableNotificationContent()
        content.title = "Backup can’t reach Photostead"
        content.body = "It hasn’t for a day, and photos and videos are waiting. Open Photostead to see why."
        content.threadIdentifier = "backup"
        await add(ID.stuck, content, at: at)
    }

    /// Photostead answered: a notice about not reaching it, set or shown, is taken back.
    func reached() {
        guard clock.unreachableSince != nil, clock.reached() else { return }
        center.removePendingNotificationRequests(withIdentifiers: [ID.stuck])
        center.removeDeliveredNotifications(withIdentifiers: [ID.stuck])
    }

    /// Turned off, paired again or unpaired: nothing set, nothing counted.
    func forget() {
        clock = NoticeClock()
        center.removePendingNotificationRequests(withIdentifiers: [ID.backedUp, ID.stuck])
        center.removeDeliveredNotifications(withIdentifiers: [ID.stuck])
    }

    private func add(_ id: String, _ content: UNNotificationContent, at: Date) async {
        let wait = at.timeIntervalSinceNow
        let trigger = wait > 1 ? UNTimeIntervalNotificationTrigger(timeInterval: wait, repeats: false) : nil
        // The same id replaces one still to show (with the new count, at the same time).
        try? await center.add(UNNotificationRequest(identifier: id, content: content, trigger: trigger))
    }
}
