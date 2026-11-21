// Photostead for iPhone: backs up the Photos library to your own Photostead server. Paired with a
// code from the web app; finds the server at home, or away over Tailscale or Funnel; sends originals with
// resumable uploads, newest first, and only what the server doesn't have. iOS sends them (the
// outbox), so they carry on while the app is closed; iOS's background tasks get more ready.

import BackgroundTasks
import SwiftUI

@main
struct PhotosteadApp: App {
    @UIApplicationDelegateAdaptor private var delegate: AppDelegate
    @State private var backup = Backup.shared
    @State private var library = Library.shared
    @State private var tab = "library"
    @State private var toTop = 0
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        WindowGroup {
            Group {
                if backup.pairing == nil {
                    PairView()
                } else if backup.welcoming {
                    Welcome() // just paired: Photos, how backup goes, notices (once)
                        .transition(.opacity)
                } else {
                    TabView(selection: Binding(get: { tab }, set: { picked in
                        if picked == tab && picked == "library" { toTop += 1 } // tapped again: back to the top
                        tab = picked
                    })) {
                        LibraryView(toTop: toTop).tabItem { Label("Library", systemImage: "photo.on.rectangle") }.tag("library")
                        CollectionsView().tabItem { Label("Collections", systemImage: "rectangle.stack") }.tag("collections")
                        SearchView().tabItem { Label("Search", systemImage: "magnifyingglass") }.tag("search")
                    }
                }
            }
            .animation(.easeInOut(duration: 0.3), value: backup.welcoming)
            .environment(backup)
            .environment(library)
            .onOpenURL { backup.opened($0) }
            .onChange(of: phase) { _, now in
                switch now {
                case .active:
                    Notices.shared.opened() // the app shows how the backup went: no notice about it still to come
                    backup.refreshCounts()
                    backup.start() // opening the app backs up what's new
                    Task { await library.sync() } // and catches up with the library
                case .background:
                    BackgroundWork.schedule()
                default:
                    break
                }
            }
        }
    }
}

final class AppDelegate: NSObject, UIApplicationDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        BackgroundWork.register()
        return true
    }

    /// iOS woke the app because transfers it was sending finished (perhaps the app was closed).
    func application(_ application: UIApplication, handleEventsForBackgroundURLSession identifier: String, completionHandler: @escaping () -> Void) {
        guard identifier == Outbox.identifier else { return completionHandler() }
        // Once it's all noted, the backup notices hear of it (the last of a backup may just have
        // gone), and only then is iOS told the app is done.
        Outbox.shared.eventsDone = {
            Task { @MainActor in
                await Backup.shared.noteRun()
                completionHandler()
            }
        }
        MainActor.assumeIsolated { _ = Backup.shared } // hears what finished, and notes it
    }
}

/// The background tasks iOS runs when it suits it: a long one when the phone is idle (usually
/// overnight, charging), and a short look every so often. Both get more items ready for the
/// outbox, and send again what stopped.
enum BackgroundWork {
    static let backup = "studio.atomtan.photostead.backup"
    static let refresh = "studio.atomtan.photostead.refresh"

    static func register() {
        BGTaskScheduler.shared.register(forTaskWithIdentifier: backup, using: nil) { handle($0, for: nil) }
        BGTaskScheduler.shared.register(forTaskWithIdentifier: refresh, using: nil) { handle($0, for: 25) }
    }

    static func schedule() {
        let long = BGProcessingTaskRequest(identifier: backup)
        long.requiresNetworkConnectivity = true
        try? BGTaskScheduler.shared.submit(long)
        let short = BGAppRefreshTaskRequest(identifier: refresh)
        short.earliestBeginDate = Date(timeIntervalSinceNow: 30 * 60)
        try? BGTaskScheduler.shared.submit(short)
    }

    private static func handle(_ task: BGTask, for seconds: TimeInterval?) {
        schedule() // the next ones
        let work = Task { @MainActor in
            await Backup.shared.runInBackground(until: seconds.map { Date(timeIntervalSinceNow: $0) })
            task.setTaskCompleted(success: true)
        }
        task.expirationHandler = { work.cancel() }
    }
}
