// Backing up: every photo and video in the Photos library, newest first, that isn't in Photostead
// yet. Each original goes (a Live Photo's photo and its clip, which the server pairs again); a
// photo's edits stay on the phone. Getting an item ready (reading it out of Photos, hashing it,
// asking the server) needs the app running: while it's open, for a little after it's left, and in
// the background tasks iOS gives it (see BackgroundWork). Sending is iOS's (the outbox), so it
// carries on while the app is suspended or closed. What's done and what's out is noted on the phone
// (the ledger); Photostead's own check means a reinstalled app doesn't send anything twice either.

import Foundation
import Network
import Observation
import Photos
import UIKit

/// What's backed up, by the Photos library's id for each item, and what's out with iOS. Kept in
/// Application Support.
actor Ledger {
    struct Done: Codable {
        var hashes: [String]
        var at: Date
    }

    /// One file on its way: its item, its file in the outbox, and its upload on the server.
    struct Transfer: Codable {
        var asset: String
        var file: String // the whole file, in the outbox
        var fileName: String
        var sha256: String
        var size: Int64
        var takenAt: Date?
        var location: String // "" until it's made on the server (again)
        var item: String? // the phone's name for it (deviceItem), sent with it
        var sending: String? // the file iOS has: the whole one, what's left of it, or a piece
        var waiting: String? // why it isn't being sent: tried again later
        var piece: Int64? // the most Photostead takes in one request (through Cloudflare): sent in pieces
    }

    struct Pending: Codable {
        var keys: [String]
        var hashes: [String]
    }

    private struct Saved: Codable {
        var done: [String: Done] = [:]
        var pending: [String: Pending] = [:]
        var transfers: [String: Transfer] = [:]
        var failed: [String: String] = [:]
    }

    private var s: Saved
    private let file: URL

    init() {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        file = dir.appending(path: "ledger.json")
        s = (try? Data(contentsOf: file)).flatMap { try? JSONDecoder().decode(Saved.self, from: $0) } ?? Saved()
    }

    func save() {
        if let data = try? JSONEncoder().encode(s) { try? data.write(to: file, options: .atomic) }
    }

    /// Backed up, out with iOS, or failed (tried again on the next Back up now): not to get ready.
    func settled(_ asset: String) -> Bool { s.done[asset] != nil || s.pending[asset] != nil || s.failed[asset] != nil }
    func full(count: Int, bytes: Int64) -> Bool { s.transfers.count >= count || outBytes >= bytes }
    func doneCount(of ids: Set<String>) -> Int { ids.reduce(0) { $0 + (s.done[$1] != nil ? 1 : 0) } }
    func failedCount(of ids: Set<String>) -> Int { ids.reduce(0) { $0 + (s.failed[$1] != nil ? 1 : 0) } }
    var lastFailure: String? { s.failed.values.first }

    var transfers: [String: Transfer] { s.transfers }
    var outBytes: Int64 { s.transfers.values.reduce(0) { $0 + $1.size } }
    func transfer(_ key: String) -> Transfer? { s.transfers[key] }

    /// An item got ready: done at once if the server had all of it, else out until its transfers are.
    func queued(_ asset: String, keys: [String], hashes: [String], transfers: [String: Transfer]) -> Bool {
        s.failed[asset] = nil
        s.transfers.merge(transfers) { $1 }
        if keys.isEmpty {
            s.done[asset] = Done(hashes: hashes, at: Date())
        } else {
            s.pending[asset] = Pending(keys: keys, hashes: hashes)
        }
        save()
        return keys.isEmpty
    }

    /// Each item done, with its files' hashes: to tell Photostead which of the phone's items it has.
    func doneItems() -> [(asset: String, hashes: [String])] { s.done.map { ($0.key, $0.value.hashes) } }

    func update(_ key: String, _ change: (inout Transfer) -> Void) {
        guard var t = s.transfers[key] else { return }
        change(&t)
        s.transfers[key] = t
        save()
    }

    /// A transfer arrived: true when that was the last of its item.
    func arrived(_ key: String) -> Bool {
        guard let t = s.transfers.removeValue(forKey: key), var p = s.pending[t.asset] else { save(); return false }
        p.keys.removeAll { $0 == key }
        if p.keys.isEmpty {
            s.pending[t.asset] = nil
            s.done[t.asset] = Done(hashes: p.hashes, at: Date())
        } else {
            s.pending[t.asset] = p
        }
        save()
        return p.keys.isEmpty
    }

    /// The server turned a transfer down: its item is noted as failed (tried again on the next
    /// Back up now), and the rest of it isn't sent. Returns the transfers dropped.
    func refused(_ key: String, because why: String) -> [String: Transfer] {
        guard let t = s.transfers[key] else { return [:] }
        let dropped = s.transfers.filter { $0.value.asset == t.asset }
        for k in dropped.keys { s.transfers[k] = nil }
        s.pending[t.asset] = nil
        s.failed[t.asset] = why
        save()
        return dropped
    }

    func clearFailed() {
        s.failed = [:]
        save()
    }

    func forget() {
        s = Saved()
        save()
    }
}

@MainActor
@Observable
final class Backup {
    static let shared = Backup()

    enum Phase: Equatable {
        case idle, preparing, waitingForWiFi, unreachable, signedOut
    }

    struct Flight: Equatable {
        var name: String
        var from: Int64 // where the part iOS is sending starts
        var sent: Int64
        var size: Int64
    }

    var pairing: Pairing? = Pairing.load()

    /// An item this app put in the Photos library (saved from Photostead): it came from there, so
    /// it isn't sent back.
    func cameFromPhotostead(_ asset: String) async {
        _ = await ledger.queued(asset, keys: [], hashes: [], transfers: [:])
    }
    var access: PHAuthorizationStatus = PHPhotoLibrary.authorizationStatus(for: .readWrite)
    var total = 0
    var backedUp = 0
    var failed = 0
    var phase: Phase = .idle
    var preparingName: String?
    var inFlight: [String: Flight] = [:] // with iOS now, by transfer
    var message = ""
    var lastRun: Date?
    var addressInUse: URL?
    var incoming: PairingLink? // a pairing link opened from outside, waiting to be confirmed
    /// Paired just now, on a phone that hasn't had the first run (Welcome.swift): it shows before
    /// the library. Once per phone, so pairing again (or with another Photostead) goes straight in.
    var welcoming: Bool = UserDefaults.standard.bool(forKey: "welcoming") {
        didSet {
            UserDefaults.standard.set(welcoming, forKey: "welcoming")
            if !welcoming { UserDefaults.standard.set(true, forKey: "welcomed") }
        }
    }

    var includeVideos: Bool = UserDefaults.standard.object(forKey: "includeVideos") as? Bool ?? true {
        didSet { UserDefaults.standard.set(includeVideos, forKey: "includeVideos"); refreshCounts(); updateSystemUploads() }
    }
    var useCellular: Bool = UserDefaults.standard.bool(forKey: "useCellular") {
        didSet { UserDefaults.standard.set(useCellular, forKey: "useCellular"); updateSystemUploads() }
    }
    /// Off: nothing is sent (browsing the library still works).
    var backupOn: Bool = UserDefaults.standard.object(forKey: "backupOn") as? Bool ?? true {
        didSet {
            UserDefaults.standard.set(backupOn, forKey: "backupOn")
            if backupOn { start() } else { stop(); Notices.shared.forget() } // nothing to tell of a backup that's off
            updateSystemUploads()
        }
    }
    /// iOS sends new photos itself (iOS 27 and later, with full access to Photos), as it does for
    /// iCloud Photos: the extension in Uploads/ says what.
    var systemUploads = false

    var preparing: Bool { task != nil }
    var busy: Bool { preparing || !inFlight.isEmpty }
    var remaining: Int { max(0, total - backedUp) }

    /// How many transfers, and how many bytes, may be out with iOS at once.
    private let maxOut = 12
    private let maxOutBytes: Int64 = 2 << 30

    private var task: Task<Void, Never>?
    private let ledger = Ledger()
    private let path = NWPathMonitor()
    private var onExpensive = false

    private init() {
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("-reset") { // UI tests start unpaired, with nothing backed up
            Pairing.forget()
            pairing = nil
            Task { [ledger] in
                await ledger.forget()
                await Outbox.shared.cancelAll()
            }
            try? FileManager.default.removeItem(at: Outbox.directory)
            try? FileManager.default.createDirectory(at: Outbox.directory, withIntermediateDirectories: true)
            welcoming = false // (its didSet runs here too, so before the settings go)
            for key in ["welcoming", "welcomed", "notifyBackups", "noticeClock"] { UserDefaults.standard.removeObject(forKey: key) } // a new phone's first run
        }
        if ProcessInfo.processInfo.arguments.contains("-demoLibrary") { // the library screens, with no server
            pairing = Pairing(serverID: "demo", serverName: "Demo", addresses: [], deviceName: "iPhone", account: "Demo")
        }
        if ProcessInfo.processInfo.arguments.contains("-welcome") { welcoming = true } // the first run, as if just paired (with -demoLibrary: no server)
        #endif
        path.pathUpdateHandler = { [weak self] p in
            let expensive = p.isExpensive || p.isConstrained
            Task { @MainActor in self?.onExpensive = expensive }
        }
        path.start(queue: .global(qos: .utility))
        let outbox = Outbox.shared
        outbox.onFinished = { [weak self] done in await self?.finished(done) }
        outbox.onProgress = { [weak self] key, sent in
            if let f = self?.inFlight[key] { self?.inFlight[key]?.sent = f.from + sent }
        }
        _ = outbox.session // to hear about transfers from before a relaunch
    }

    // MARK: Pairing

    func pair(urls: [URL], code: String, name: String) async throws {
        let (pairing, token) = try await Server.pair(urls: urls, code: code, name: name)
        await forgetTransfers()
        // The phone's copy of the library starts again, even from the same server: it may be
        // someone else's account now, whose albums and roles aren't the last person's. Unpairing
        // erases it already; this makes sure, whatever led here (the UI tests' -reset doesn't).
        await Library.shared.forget()
        Notices.shared.forget() // another server's news
        Keychain.token = token
        pairing.save()
        welcoming = !UserDefaults.standard.bool(forKey: "welcomed") // before the pairing, so the library doesn't flash up first
        self.pairing = pairing
        incoming = nil
        phase = .idle
        message = ""
        refreshCounts()
        start()
        updateSystemUploads()
        Task { await Library.shared.sync() } // the library at once, not after the first backup
    }

    func unpair() {
        stop()
        Pairing.forget()
        pairing = nil
        Notices.shared.forget()
        updateSystemUploads()
        addressInUse = nil
        Task {
            await forgetTransfers()
            await Library.shared.forget()
        }
        refreshCounts()
    }

    private func forgetTransfers() async {
        await Outbox.shared.cancelAll()
        await ledger.forget() // another server: what's on it isn't known yet
        inFlight = [:]
        try? FileManager.default.removeItem(at: Outbox.directory)
        try? FileManager.default.createDirectory(at: Outbox.directory, withIntermediateDirectories: true)
    }

    func opened(_ url: URL) {
        if let link = PairingLink(url.absoluteString) { incoming = link }
    }

    // MARK: Photos

    func requestAccess() async {
        access = await PHPhotoLibrary.requestAuthorization(for: .readWrite)
        refreshCounts()
        if canRead { start() }
        updateSystemUploads()
    }

    var canRead: Bool { access == .authorized || access == .limited }

    private func fetchAssets() -> PHFetchResult<PHAsset> { backupFetch(videos: includeVideos) }

    // MARK: iOS's own uploads

    /// Turns iOS's background uploads on when backing up is (paired, full access to Photos), with
    /// the same settings, and off otherwise. Once on, Photostead is told which of the phone's items
    /// it has already, so iOS doesn't send them again.
    func updateSystemUploads() {
        guard #available(iOS 27.0, *) else { return }
        #if targetEnvironment(simulator)
        return // iOS doesn't run them in the Simulator
        #else
        let library = PHPhotoLibrary.shared()
        let want = backupOn && pairing != nil && PHPhotoLibrary.authorizationStatus(for: .readWrite) == .authorized
        BackupOptions(videos: includeVideos).save()
        do {
            if want {
                let options = PHAssetResourceUploadJobOptions()
                options.preventsExpensiveNetworkAccess = !useCellular
                if library.uploadJobExtensionEnabled {
                    try library.setUploadJobExtensionOptions(options)
                } else {
                    try library.enableUploadJobExtension(with: options)
                }
            } else if library.uploadJobExtensionEnabled {
                try library.disableUploadJobExtension()
            }
        } catch {
            message = "iOS wouldn't back up in the background: \(error.localizedDescription)"
        }
        systemUploads = library.uploadJobExtensionEnabled
        if systemUploads { Task { await noteSent() } }
        #endif
    }

    /// Tells Photostead which of the phone's items were sent before uploads named them (once for
    /// each server), so iOS's uploads skip them.
    private func noteSent() async {
        guard let pairing, let token = Keychain.token else { return }
        let flag = "sentNoted-\(pairing.serverID)"
        guard !UserDefaults.standard.bool(forKey: flag) else { return }
        let server = Server(pairing: pairing, token: token)
        let done = await ledger.doneItems().filter { !$0.hashes.isEmpty }
        do {
            for start in stride(from: 0, to: done.count, by: 1000) {
                let batch = done[start..<min(start + 1000, done.count)].map { DeviceItems.Item(item: $0.asset, sha256: Array($0.hashes.prefix(10))) }
                _ = try await server.call("POST", "api/v1/uploads/device-items", body: DeviceItems(items: batch), as: DeviceItems.Recorded.self)
            }
            UserDefaults.standard.set(true, forKey: flag)
        } catch {
            // next time
        }
    }

    func refreshCounts() {
        access = PHPhotoLibrary.authorizationStatus(for: .readWrite)
        guard canRead else {
            total = 0
            backedUp = 0
            failed = 0
            return
        }
        let assets = fetchAssets()
        var ids = Set<String>()
        assets.enumerateObjects { a, _, _ in ids.insert(a.localIdentifier) }
        total = ids.count
        Task {
            backedUp = await ledger.doneCount(of: ids)
            failed = await ledger.failedCount(of: ids)
        }
    }

    // MARK: Running

    /// Backs up now, in the foreground (and for a little after the app is left). Items that failed
    /// before are tried again.
    func start() {
        updateSystemUploads()
        guard task == nil, pairing != nil, canRead, backupOn else { return }
        task = Task { [weak self] in
            guard let self else { return }
            let background = UIApplication.shared.beginBackgroundTask(withName: "backup") { [weak self] in self?.task?.cancel() }
            await self.ledger.clearFailed()
            await self.run(until: nil)
            await self.noteRun()
            UIApplication.shared.endBackgroundTask(background)
            self.task = nil
        }
    }

    /// Backs up in a background task iOS gave the app, until the deadline (or iOS ends it).
    func runInBackground(until deadline: Date?) async {
        guard task == nil, pairing != nil, canRead, backupOn else { return }
        let work = Task {
            await self.run(until: deadline)
            await self.noteRun()
        }
        task = work
        await withTaskCancellationHandler { await work.value } onCancel: { work.cancel() }
        task = nil
    }

    /// Stops getting items ready, and has iOS stop sending (what arrived stays: it carries on from
    /// there next time).
    func stop() {
        task?.cancel()
        task = nil
        preparingName = nil
        Task { await Outbox.shared.cancelAll() }
        if phase == .preparing { phase = .idle }
    }

    // MARK: Notices

    /// After a run, and once iOS's news of what it sent is all noted (PhotosteadApp's AppDelegate):
    /// the backup notices (Notices.swift) hear how it went, before iOS suspends the app. Photostead
    /// out of reach: a notice for a day on, if items wait. In the background, with nothing left out
    /// with iOS: what was sent, if anything was.
    func noteRun() async {
        let notices = Notices.shared
        guard notices.on else { return }
        if phase == .unreachable { await notices.unreachable(waiting: await notBackedUp()) }
        if notices.untold, UIApplication.shared.applicationState == .background, await ledger.transfers.isEmpty {
            await notices.finished(left: await notBackedUp())
        }
    }

    /// How many items aren't backed up yet, counted afresh: when iOS wakes the app for a moment, the
    /// counts on screen haven't been.
    private func notBackedUp() async -> Int {
        guard canRead else { return 0 }
        var ids = Set<String>()
        fetchAssets().enumerateObjects { a, _, _ in ids.insert(a.localIdentifier) }
        return ids.count - (await ledger.doneCount(of: ids))
    }

    private func run(until deadline: Date?) async {
        if let saved = Pairing.load(), saved.serverID == pairing?.serverID { pairing = saved } // addresses learned since
        guard let pairing, let token = Keychain.token else { return }
        if onExpensive && !useCellular {
            phase = .waitingForWiFi
            message = "Waiting for Wi-Fi (or allow cellular data in Settings below)."
            return
        }
        let server = Server(pairing: pairing, token: token)
        phase = .preparing
        message = ""
        do {
            addressInUse = try await server.base()
            Notices.shared.reached()
        } catch {
            phase = .unreachable
            message = error.localizedDescription
            return
        }
        await resendWaiting(server: server, token: token)

        let assets = fetchAssets()
        var todo: [PHAsset] = []
        for i in 0..<assets.count {
            let a = assets.object(at: i)
            if await !ledger.settled(a.localIdentifier) { todo.append(a) }
        }
        let late = { deadline.map { Date() >= $0 } ?? false }
        var withIOS = 0 // on their way with iOS's own uploads
        work: for start in stride(from: 0, to: todo.count, by: 200) {
            let page = Array(todo[start..<min(start + 200, todo.count)])
            let elsewhere = await elsewhere(page, server: server)
            for asset in page {
                // Room with iOS for more: wait for some to arrive.
                while await ledger.full(count: maxOut, bytes: maxOutBytes) {
                    if Task.isCancelled || late() { break }
                    try? await Task.sleep(for: .seconds(1))
                }
                if Task.isCancelled || late() || phase == .signedOut { break work }
                if onExpensive && !useCellular {
                    phase = .waitingForWiFi
                    message = "Waiting for Wi-Fi."
                    break work
                }
                // Sent already (by iOS's own uploads): nothing to read. On its way (iOS is sending it):
                // next time. Only queued with iOS: sent from here, and Photostead turns iOS's copy away.
                let items = backupResources(for: asset).map { deviceItem(asset.localIdentifier, $0.type) }
                if elsewhere.known.contains(asset.localIdentifier) || (!items.isEmpty && items.allSatisfy(elsewhere.known.contains)) {
                    if await ledger.queued(asset.localIdentifier, keys: [], hashes: [], transfers: [:]) { backedUp += 1 }
                    continue
                }
                if items.contains(where: elsewhere.sending.contains) {
                    withIOS += 1
                    continue
                }
                do {
                    try await prepare(asset, server: server, token: token)
                } catch is CancellationError {
                    break work
                } catch let error as PhotosteadError {
                    if case .unreachable = error {
                        phase = .unreachable
                        message = error.localizedDescription
                        break work
                    }
                    message = error.localizedDescription
                } catch {
                    message = error.localizedDescription
                    if let code = (error as? URLError)?.code, [.notConnectedToInternet, .networkConnectionLost, .cannotConnectToHost, .timedOut].contains(code) {
                        phase = .unreachable
                        await server.forgetReached()
                        break work
                    }
                }
            }
        }
        await ledger.save()
        preparingName = nil
        lastRun = Date()
        if phase == .preparing {
            phase = .idle
            if withIOS > 0 && message.isEmpty {
                message = withIOS == 1 ? "iOS is sending 1 in the background." : "iOS is sending \(withIOS) in the background."
            }
        }
        refreshCounts()
        await Library.shared.sync() // what just went up shows in the library
    }

    /// What Photostead has of these items and what's on its way to it, by the phone's names for
    /// them (nothing known, when it can't say: the files are hashed and asked about as before).
    private func elsewhere(_ assets: [PHAsset], server: Server) async -> (known: Set<String>, sending: Set<String>) {
        let names = assets.flatMap { a in [a.localIdentifier] + backupResources(for: a).map { deviceItem(a.localIdentifier, $0.type) } }
        guard let check = try? await server.call("POST", "api/v1/uploads/device-items/check", body: DeviceCheck(items: names), as: DeviceKnown.self) else { return ([], []) }
        return (Set(check.known), Set(check.sending ?? []))
    }

    /// One item: each of its originals read out and hashed, asked about, and the ones the server
    /// doesn't have handed to iOS to send.
    private func prepare(_ asset: PHAsset, server: Server, token: String) async throws {
        let wanted = backupResources(for: asset)
        var hashes: [String] = []
        var keys: [String] = []
        var transfers: [String: Ledger.Transfer] = [:]
        var made: [URL] = []
        var there: [DeviceItems.Item] = [] // files Photostead had: named for it, so iOS's uploads skip them
        do {
            for resource in wanted {
                try Task.checkCancellation()
                preparingName = resource.originalFilename
                let name = UUID().uuidString
                let file = Outbox.directory.appending(path: name)
                made.append(file)
                let exported = try await export(resource, to: file)
                hashes.append(exported.sha256)
                struct Check: Encodable { var sha256: [String] }
                struct Known: Decodable { var known: [String] }
                let known = try await server.call("POST", "api/v1/uploads/check", body: Check(sha256: [exported.sha256]), as: Known.self)
                let item = deviceItem(asset.localIdentifier, resource.type)
                if known.known.contains(exported.sha256) {
                    try? FileManager.default.removeItem(at: file)
                    there.append(DeviceItems.Item(item: item, sha256: [exported.sha256]))
                    continue
                }
                let made = try await Resumable.create(server, size: exported.size,
                                                      meta: UploadMeta(fileName: resource.originalFilename, takenAt: asset.creationDate, sha256: exported.sha256, deviceItem: item))
                let key = UUID().uuidString
                keys.append(key)
                transfers[key] = Ledger.Transfer(asset: asset.localIdentifier, file: name, fileName: resource.originalFilename, sha256: exported.sha256,
                                                 size: exported.size, takenAt: asset.creationDate, location: made.location, item: item, sending: name, piece: made.piece)
            }
        } catch {
            for f in made { try? FileManager.default.removeItem(at: f) }
            throw error
        }
        if !there.isEmpty { _ = try? await server.call("POST", "api/v1/uploads/device-items", body: DeviceItems(items: there), as: DeviceItems.Recorded.self) }
        // Noted before iOS has them: a transfer that finishes at once finds its record.
        if await ledger.queued(asset.localIdentifier, keys: keys, hashes: hashes, transfers: transfers) {
            backedUp += 1
            return
        }
        let base = try await server.base()
        for (key, t) in transfers {
            try await handOver(key, t, from: 0, to: base, token: token)
        }
    }

    /// Gives iOS what's next of a transfer, from offset: the rest of it, or, when Photostead takes
    /// only so much in one request (through Cloudflare), the next piece.
    private func handOver(_ key: String, _ t: Ledger.Transfer, from offset: Int64, to base: URL, token: String) async throws {
        let length = t.piece.map { min($0, t.size - offset) } ?? t.size - offset
        let last = offset + length >= t.size
        var sending = t.file
        if offset > 0 || !last {
            sending = last ? "\(t.file)-from-\(offset)" : "\(t.file)-from-\(offset)-\(length)"
            try slice(of: Outbox.directory.appending(path: t.file), from: offset, length: last ? nil : length, to: Outbox.directory.appending(path: sending))
        }
        if let old = t.sending, old != t.file, old != sending { try? FileManager.default.removeItem(at: Outbox.directory.appending(path: old)) }
        let file = sending
        await ledger.update(key) {
            $0.sending = file
            $0.waiting = nil
        }
        inFlight[key] = Flight(name: t.fileName, from: offset, sent: offset, size: t.size)
        Outbox.shared.send(key: key, file: Outbox.directory.appending(path: sending), to: base.appending(path: Resumable.path(t.location)),
                           offset: offset, complete: last, token: token)
    }

    /// What iOS said about a transfer it finished (perhaps while the app was closed).
    func finished(_ done: Outbox.Finished) async {
        inFlight[done.key] = nil
        guard let t = await ledger.transfer(done.key) else { return }
        let ok = done.error == nil && (200..<300).contains(done.status)
        switch done.status {
        case 204 where done.error == nil && (done.offset ?? 0) < t.size: // a piece arrived: the next one now, even in the background
            await nextPiece(done.key, t)
        case _ where ok:
            remove(t)
            if await ledger.arrived(done.key) {
                backedUp += 1
                Notices.shared.arrived(t.asset)
                if task == nil { await noteRun() } // perhaps the last of a backup, with no run left to say so
            }
        case 409: // the server has a different amount: carry on from there
            await ledger.update(done.key) { $0.waiting = "Carrying on from where Photostead got to." }
            await resendSoon()
        case 401:
            phase = .signedOut
            message = "This phone was signed out of Photostead. Pair it again to carry on."
        case 404: // the upload ran out on the server: make it again
            await ledger.update(done.key) { $0.location = ""; $0.waiting = "Starting it again." }
            await resendSoon()
        case 400, 403, 413, 415, 422:
            let why = problemDetail(done.body)
            for (_, dropped) in await ledger.refused(done.key, because: why.isEmpty ? "Photostead turned it down (\(done.status))." : why) {
                remove(dropped)
            }
            failed += 1
        default: // no answer, or the server's trouble: try again
            await ledger.update(done.key) { $0.waiting = done.error ?? "Photostead had trouble (\(done.status))." }
            await resendSoon()
        }
    }

    private func remove(_ t: Ledger.Transfer) {
        try? FileManager.default.removeItem(at: Outbox.directory.appending(path: t.file))
        if let sending = t.sending, sending != t.file { try? FileManager.default.removeItem(at: Outbox.directory.appending(path: sending)) }
    }

    /// After a piece arrives: the next one, from where Photostead got to.
    private func nextPiece(_ key: String, _ t: Ledger.Transfer) async {
        guard let pairing, let token = Keychain.token else { return }
        do {
            try await resend(key, t, server: Server(pairing: pairing, token: token), token: token)
        } catch {
            await ledger.update(key) { $0.waiting = error.localizedDescription }
            await resendSoon()
        }
    }

    /// When the app is open (or being given time), transfers that stopped go again shortly.
    private func resendSoon() async {
        guard UIApplication.shared.applicationState != .background || task != nil else { return }
        guard let pairing, let token = Keychain.token else { return }
        try? await Task.sleep(for: .seconds(3))
        await resendWaiting(server: Server(pairing: pairing, token: token), token: token)
    }

    /// Transfers iOS isn't sending (they stopped, or the app was swiped away) go again: from where
    /// the server got to, as a new upload if it ran out.
    func resendWaiting(server: Server, token: String) async {
        let sending = await Outbox.shared.sending()
        for (key, t) in await ledger.transfers where !sending.contains(key) {
            if Task.isCancelled { return }
            do {
                try await resend(key, t, server: server, token: token)
            } catch {
                await ledger.update(key) { $0.waiting = error.localizedDescription }
                if (error as? PhotosteadError).map({ if case .unreachable = $0 { true } else { false } }) == true { return }
            }
        }
    }

    private func resend(_ key: String, _ t: Ledger.Transfer, server: Server, token: String) async throws {
        let whole = Outbox.directory.appending(path: t.file)
        guard FileManager.default.fileExists(atPath: whole.path) else {
            for (_, dropped) in await ledger.refused(key, because: "Its file on the phone went missing.") { remove(dropped) }
            return
        }
        var t = t
        var offset: Int64 = 0
        if !t.location.isEmpty {
            if let found = try await Resumable.offset(server, location: t.location) {
                if found.complete { // it got there; only the answer was lost
                    remove(t)
                    if await ledger.arrived(key) {
                        backedUp += 1
                        Notices.shared.arrived(t.asset)
                    }
                    return
                }
                offset = found.offset
                t.piece = found.piece // as Photostead says at this address
            } else {
                t.location = "" // ran out on the server
            }
        }
        if t.location.isEmpty {
            (t.location, t.piece) = try await Resumable.create(server, size: t.size, meta: UploadMeta(fileName: t.fileName, takenAt: t.takenAt, sha256: t.sha256, deviceItem: t.item))
            offset = 0
        }
        let (at, piece) = (t.location, t.piece)
        await ledger.update(key) {
            $0.location = at
            $0.piece = piece
        }
        try await handOver(key, t, from: offset, to: try await server.base(), token: token)
    }
}
