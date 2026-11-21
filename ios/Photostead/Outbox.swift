// The outbox: files ready to go, sent by iOS in a background URLSession. iOS carries on with them
// while Photostead is suspended or closed (not when it's swiped away: iOS cancels them then), and
// wakes the app briefly when they finish, to note it and send the next. Each task's description is
// its transfer's key in the ledger, so the two meet again after a relaunch.

import Foundation
import UIKit

final class Outbox: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    static let shared = Outbox()
    static let identifier = "studio.atomtan.photostead.uploads"

    /// Where files wait to be sent (kept out of the phone's own backups).
    static let directory: URL = {
        var dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appending(path: "Outbox", directoryHint: .isDirectory)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try? dir.setResourceValues(values)
        return dir
    }()

    /// What a finished task said: its key, status, headers, body, and error (no response at all).
    struct Finished: Sendable {
        var key: String
        var status: Int
        var offset: Int64?
        var body: Data
        var error: String?
    }

    var onFinished: (@MainActor (Finished) async -> Void)?
    var onProgress: (@MainActor (String, Int64) -> Void)?
    /// From the app delegate, when iOS woke the app for this session: call once all is noted.
    var eventsDone: (() -> Void)?

    private let lock = NSLock()
    private var bodies: [Int: Data] = [:]
    private let noting = DispatchGroup()
    private var _session: URLSession?

    var session: URLSession {
        lock.lock()
        defer { lock.unlock() }
        if let s = _session { return s }
        let config = URLSessionConfiguration.background(withIdentifier: Outbox.identifier)
        config.sessionSendsLaunchEvents = true
        config.isDiscretionary = false
        config.timeoutIntervalForResource = 7 * 24 * 3600
        let s = URLSession(configuration: config, delegate: self, delegateQueue: nil)
        _session = s
        return s
    }

    /// Hands a file to iOS to send: a piece of an upload from offset, the last one when complete.
    func send(key: String, file: URL, to url: URL, offset: Int64, complete: Bool, token: String) {
        var request = URLRequest(url: url)
        request.httpMethod = "PATCH"
        let cellular = UserDefaults.standard.bool(forKey: "useCellular") // as the setting is when it's handed over
        request.allowsCellularAccess = cellular
        request.allowsExpensiveNetworkAccess = cellular
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        for (k, v) in Resumable.appendHeaders(offset: offset, complete: complete) { request.setValue(v, forHTTPHeaderField: k) }
        let task = session.uploadTask(with: request, fromFile: file)
        task.taskDescription = key
        task.resume()
    }

    /// The keys iOS is sending (or waiting to) now.
    func sending() async -> Set<String> {
        Set(await session.allTasks.compactMap { $0.state == .completed ? nil : $0.taskDescription })
    }

    func cancelAll() async {
        for task in await session.allTasks { task.cancel() }
    }

    // MARK: URLSessionDataDelegate

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        lock.lock()
        bodies[dataTask.taskIdentifier, default: Data()].append(data)
        lock.unlock()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didSendBodyData bytesSent: Int64, totalBytesSent: Int64, totalBytesExpectedToSend: Int64) {
        guard let key = task.taskDescription, let report = onProgress else { return }
        Task { @MainActor in report(key, totalBytesSent) }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        lock.lock()
        let body = bodies.removeValue(forKey: task.taskIdentifier) ?? Data()
        lock.unlock()
        guard let key = task.taskDescription else { return }
        let http = task.response as? HTTPURLResponse
        let done = Finished(key: key, status: http?.statusCode ?? 0, offset: http?.value(forHTTPHeaderField: "Upload-Offset").flatMap { Int64($0) },
                            body: body, error: error.map { $0.localizedDescription })
        noting.enter()
        Task { @MainActor in
            await self.onFinished?(done)
            self.noting.leave()
        }
    }

    func urlSessionDidFinishEvents(forBackgroundURLSession session: URLSession) {
        noting.notify(queue: .main) { [weak self] in
            self?.eventsDone?()
            self?.eventsDone = nil
        }
    }
}
