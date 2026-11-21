// Getting one file ready for Photostead: read out of the Photos library while it's hashed
// (SHA-256), asked about first (the server may have it already, deleted ones included), then an
// upload made on the server (IETF resumable uploads, draft-14) for the outbox to send. Asking how
// far an upload got, and starting the rest from there, is here too.

import CryptoKit
import Foundation
import Photos

/// A photo's or video's original, as a file on the phone.
struct Exported {
    var file: URL
    var sha256: String
    var size: Int64
}

/// Reads a resource's original into a file, hashing it on the way (from iCloud when it's only
/// there).
func export(_ resource: PHAssetResource, to file: URL) async throws -> Exported {
    try? FileManager.default.removeItem(at: file)
    FileManager.default.createFile(atPath: file.path, contents: nil)
    let handle = try FileHandle(forWritingTo: file)
    final class Box: @unchecked Sendable {
        var hasher = SHA256()
        var size: Int64 = 0
        var failure: Error?
    }
    let box = Box()
    let options = PHAssetResourceRequestOptions()
    options.isNetworkAccessAllowed = true
    try await withTaskCancellationHandler {
        try await withCheckedThrowingContinuation { (done: CheckedContinuation<Void, Error>) in
            PHAssetResourceManager.default().requestData(for: resource, options: options) { data in
                guard box.failure == nil else { return }
                do {
                    try handle.write(contentsOf: data)
                    box.hasher.update(data: data)
                    box.size += Int64(data.count)
                } catch {
                    box.failure = error
                }
            } completionHandler: { error in
                if let error = error ?? box.failure { done.resume(throwing: error) } else { done.resume() }
            }
        }
    } onCancel: {}
    try handle.close()
    let digest = box.hasher.finalize().map { String(format: "%02x", $0) }.joined()
    return Exported(file: file, sha256: digest, size: box.size)
}

/// Part of a file as a file of its own (a background upload sends a whole file): from offset, to the
/// end or length bytes on.
func slice(of file: URL, from offset: Int64, length: Int64? = nil, to out: URL) throws {
    try? FileManager.default.removeItem(at: out)
    FileManager.default.createFile(atPath: out.path, contents: nil)
    let reader = try FileHandle(forReadingFrom: file)
    let writer = try FileHandle(forWritingTo: out)
    defer {
        try? reader.close()
        try? writer.close()
    }
    try reader.seek(toOffset: UInt64(offset))
    var left = length ?? .max
    while left > 0, let chunk = try reader.read(upToCount: Int(min(left, 4 << 20))), !chunk.isEmpty {
        try writer.write(contentsOf: chunk)
        left -= Int64(chunk.count)
    }
}

enum Resumable {
    static let interop = "10"

    /// Makes an upload on the server: its address, and the most it takes in one request (pieceSize).
    static func create(_ server: Server, size: Int64, meta: UploadMeta) async throws -> (location: String, piece: Int64?) {
        var request = try await server.request("POST", "api/v1/uploads/resumable", headers: [
            "Upload-Complete": "?0", "Upload-Length": String(size), "Upload-Draft-Interop-Version": interop, "Photostead-Meta": meta.header,
        ])
        request.httpBody = Data()
        let (data, response) = try await server.session.data(for: request)
        let http = response as? HTTPURLResponse
        guard http?.statusCode == 201, let location = http?.value(forHTTPHeaderField: "Location") else {
            throw PhotosteadError.refused(http?.statusCode ?? 0, problemDetail(data))
        }
        return (location, pieceSize(http?.value(forHTTPHeaderField: "Upload-Limit")))
    }

    /// How much of an upload the server has, whether it's complete, and the most it takes in one
    /// request here. nil: it's gone (ran out).
    static func offset(_ server: Server, location: String) async throws -> (offset: Int64, complete: Bool, piece: Int64?)? {
        let request = try await server.request("HEAD", path(location), headers: ["Upload-Draft-Interop-Version": interop])
        let (_, response) = try await server.session.data(for: request)
        let http = response as? HTTPURLResponse
        if http?.statusCode == 404 { return nil }
        guard http?.statusCode == 204, let offset = http?.value(forHTTPHeaderField: "Upload-Offset").flatMap({ Int64($0) }) else {
            throw PhotosteadError.refused(http?.statusCode ?? 0, "")
        }
        return (offset, http?.value(forHTTPHeaderField: "Upload-Complete") == "?1", pieceSize(http?.value(forHTTPHeaderField: "Upload-Limit")))
    }

    /// The headers for sending a piece of an upload from offset: the rest of it (complete), or not.
    static func appendHeaders(offset: Int64, complete: Bool) -> [String: String] {
        ["Content-Type": "application/partial-upload", "Upload-Offset": String(offset), "Upload-Complete": complete ? "?1" : "?0",
         "Upload-Draft-Interop-Version": interop]
    }

    /// An upload's path on the server, for appending to the server's address.
    static func path(_ location: String) -> String { String(location.drop(while: { $0 == "/" })) }
}
