// iOS's own background uploads (iOS 27 and later). iOS sends the phone's photos and videos to
// Photostead itself, whenever it suits the phone (on Wi-Fi unless cellular is allowed, idle,
// charged enough), even when the app hasn't been opened for days, as it does for iCloud Photos.
// This extension says what to send: what Photostead doesn't have yet, newest first, and that the
// app isn't sending already. It can't hash a file to ask (iOS reads the file, not the extension),
// so each file goes with the phone's name for it (deviceItem), and Photostead is asked by those
// names: which it has, and which are on their way. iOS speaks resumable uploads (an earlier
// draft, which Photostead understands), so a big video carries on where it stopped.
//
// It shares the pairing and the token with the app through the Keychain; anything of its own (what
// it gave up on) stays in its own settings.

import ExtensionFoundation
import Foundation
import Photos

@main
final class Uploads: PHBackgroundResourceUploadJobExtension {
    required init() {}

    /// iOS asked it to stop (it may at any time): finish the step under way, start nothing new.
    private var stopping = false

    /// How many items in a row Photostead already has before the rest (older) is taken to be
    /// there too: the app sends what's older, newest first, and so does this.
    private let enoughInARow = 300

    func processJobs() async -> PHBackgroundResourceUploadProcessingResult {
        stopping = false
        guard PHPhotoLibrary.authorizationStatus(for: .readWrite) == .authorized,
              let pairing = Pairing.load(), let token = Keychain.token else { return .completed }
        let server = Server(pairing: pairing, token: token)
        do {
            var piece: Int64? // how much Photostead takes in one request there, if it says
            if let at = try? await base(server) { piece = await server.pieceLimit(at: at) }
            let settled = try await settle(server, token: token, piece: piece)
            if stopping { return settled > 0 ? .processing : .completed }
            let made = try await queue(server, token: token, piece: piece)
            return settled + made > 0 ? .processing : .completed
        } catch let error as PHPhotosError where error.code == .limitExceeded {
            return .processing // as many out as iOS takes: again once some are done
        } catch {
            return .failure
        }
    }

    func willTerminate() async {
        stopping = true
    }

    // MARK: Finished

    /// Sends again what failed for a reason that can pass (to the address that answers now), and
    /// lets go of the rest, finished or not: iOS keeps only so many. Not what Photostead has, or
    /// the app is sending (Photostead turns those away: the app sent it while it waited here).
    private func settle(_ server: Server, token: String, piece: Int64?) async throws -> Int {
        var count = 0
        let retry = PHAssetResourceUploadJob.fetchJobs(action: .retry, options: nil)
        let failed = (0..<retry.count).map { retry.object(at: $0) }
        let names = failed.compactMap(name(of:))
        let check = names.isEmpty ? nil : try? await server.call("POST", "api/v1/uploads/device-items/check", body: DeviceCheck(items: names), as: DeviceKnown.self)
        let there = Set((check?.known ?? []) + (check?.sending ?? []))
        var again: [(PHAssetResourceUploadJob, URLRequest?)] = []
        for job in failed {
            if let item = name(of: job), there.contains(item) { continue } // acknowledged below
            if refusedForGood(job) || tooBig(PHAssetResource.assetResource(forUploadJob: job), piece) { continue }
            let fresh = try? await destination(server, token: token, for: job)
            again.append((job, fresh))
        }
        if !again.isEmpty {
            try await PHPhotoLibrary.shared().performChanges {
                for (job, fresh) in again { PHAssetResourceUploadJobChangeRequest(for: job)?.retry(destination: fresh) }
            }
            count += again.count
        }
        let finished = PHAssetResourceUploadJob.fetchJobs(action: .acknowledge, options: nil)
        var done: [PHAssetResourceUploadJob] = []
        for i in 0..<finished.count {
            let job = finished.object(at: i)
            if job.state == .failed, let item = name(of: job), !there.contains(item) { gaveUp[item] = Date() } // tried again tomorrow
            done.append(job)
        }
        if !done.isEmpty {
            try await PHPhotoLibrary.shared().performChanges {
                for job in done { PHAssetResourceUploadJobChangeRequest(for: job)?.acknowledge() }
            }
            count += done.count
        }
        return count
    }

    /// Bigger than Photostead takes in one request at this address (through Cloudflare): iOS sends a
    /// file in one, so the app sends it instead, in pieces. A file of unknown size is left to it too.
    private func tooBig(_ resource: PHAssetResource?, _ piece: Int64?) -> Bool {
        guard let piece, let resource else { return false }
        return Int64(resource.dataSize ?? .max) > piece
    }

    /// Photostead said no (or who's this): sending it again the same way won't help.
    private func refusedForGood(_ job: PHAssetResourceUploadJob) -> Bool {
        guard let error = job.error as? URLError else { return false }
        return [.userAuthenticationRequired, .badServerResponse, .fileDoesNotExist, .noPermissionsToReadFile].contains(error.code)
    }

    // MARK: New

    /// Fills iOS's queue with what Photostead doesn't have, newest first.
    private func queue(_ server: Server, token: String, piece: Int64?) async throws -> Int {
        let out = PHAssetResourceUploadJob.fetchJobs(action: .process, options: nil)
        var sending = Set<String>()
        for i in 0..<out.count { if let item = name(of: out.object(at: i)) { sending.insert(item) } }
        var room = PHAssetResourceUploadJob.jobLimit - out.count
        guard room > 0 else { return 0 }

        let assets = backupFetch(videos: BackupOptions.load().videos)
        let skipUntil = Date().addingTimeInterval(-86400)
        var inARow = 0
        var made = 0
        var at = 0
        while at < assets.count, room > 0, inARow < enoughInARow, !stopping {
            let page = (at..<min(at + 200, assets.count)).map { assets.object(at: $0) }
            at += page.count
            var files: [(asset: PHAsset, resource: PHAssetResource, item: String)] = []
            for asset in page {
                for resource in backupResources(for: asset) {
                    files.append((asset, resource, deviceItem(asset.localIdentifier, resource.type)))
                }
            }
            let names = page.map(\.localIdentifier) + files.map(\.item)
            let check = try await server.call("POST", "api/v1/uploads/device-items/check", body: DeviceCheck(items: names), as: DeviceKnown.self)
            let known = Set(check.known + (check.sending ?? [])) // on its way from the app: as good as there
            var jobs: [(PHAssetResource, URLRequest)] = []
            for asset in page {
                let missing = files.filter { $0.asset == asset && !known.contains($0.item) && !known.contains(asset.localIdentifier) }
                if missing.isEmpty {
                    inARow += 1
                    continue
                }
                inARow = 0
                for file in missing where room > 0 && !sending.contains(file.item) {
                    if let when = gaveUp[file.item], when > skipUntil { continue }
                    if tooBig(file.resource, piece) { continue } // the app sends it, in pieces
                    jobs.append((file.resource, try await request(server, token: token, file.asset, file.resource, item: file.item)))
                    sending.insert(file.item)
                    room -= 1
                }
            }
            if !jobs.isEmpty {
                try await PHPhotoLibrary.shared().performChanges {
                    for (resource, destination) in jobs { _ = PHAssetResourceUploadJobChangeRequest.creationRequestForJob(destination: destination, resource: resource) }
                }
                made += jobs.count
            }
        }
        return made
    }

    // MARK: Requests

    /// Where a file goes: a resumable upload on Photostead (iOS adds the upload's own headers),
    /// with the token, and what comes with the file.
    private func request(_ server: Server, token: String, _ asset: PHAsset, _ resource: PHAssetResource, item: String) async throws -> URLRequest {
        var request = URLRequest(url: try await base(server).appending(path: "api/v1/uploads/resumable"))
        request.httpMethod = "POST"
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue(UploadMeta(fileName: resource.filename ?? "", takenAt: asset.creationDate, deviceItem: item).header, forHTTPHeaderField: "Photostead-Meta")
        return request
    }

    /// A failed file's request again, to the address that answers now (it may have moved since).
    private func destination(_ server: Server, token: String, for job: PHAssetResourceUploadJob) async throws -> URLRequest? {
        guard let resource = PHAssetResource.assetResource(forUploadJob: job),
              let asset = PHAsset.fetchAssets(withLocalIdentifiers: [resource.assetLocalIdentifier], options: nil).firstObject else { return nil }
        return try await request(server, token: token, asset, resource, item: deviceItem(asset.localIdentifier, resource.type))
    }

    /// The address iOS sends to: the one this extension is declared for (BackgroundUploadURLBase),
    /// when it's one of Photostead's, else whichever answers.
    private func base(_ server: Server) async throws -> URL {
        let declared = (Bundle.main.object(forInfoDictionaryKey: "BackgroundUploadURLBase") as? String).flatMap(URL.init(string:))
        if let host = declared?.host(), let match = await server.pairing.candidates.first(where: { $0.host() == host }) { return match }
        return try await server.base()
    }

    private func name(of job: PHAssetResourceUploadJob) -> String? {
        PHAssetResource.assetResource(forUploadJob: job).map { deviceItem($0.assetLocalIdentifier, $0.type) }
    }

    /// Files whose upload failed for good, and when: left for a day.
    private var gaveUp: [String: Date] {
        get { (UserDefaults.standard.dictionary(forKey: "gaveUp") as? [String: Date] ?? [:]).filter { $0.value > Date().addingTimeInterval(-7 * 86400) } }
        set { UserDefaults.standard.set(newValue, forKey: "gaveUp") }
    }
}
