// Downloading an item at full size: into this iPhone's Photos library, or to Files. It's the
// edited photo when it's been edited on the web (what Photostead shows), otherwise the original,
// byte for byte. Something saved into Photos is noted as backed up, so it isn't sent back.

import Photos
import SwiftUI
import UIKit

enum Download {
    enum Problem: LocalizedError {
        case noAccess
        var errorDescription: String? { "Photostead isn’t allowed to add to your Photos library (Settings → Photostead → Photos)." }
    }

    /// The item as a file on the phone, at full size.
    static func file(_ item: Item, api: Server) async throws -> URL {
        let detail = try? await api.detail(item.id)
        var name = item.fileName
        if name.isEmpty { name = detail?.fileName ?? "" }
        return try await api.fullSize(item.id, named: name, edited: !item.isVideo && detail?.edited == true)
    }

    /// Into the Photos library; the file is used up.
    static func toPhotos(_ file: URL, video: Bool) async throws {
        var access = PHPhotoLibrary.authorizationStatus(for: .addOnly)
        if access == .notDetermined { access = await PHPhotoLibrary.requestAuthorization(for: .addOnly) }
        guard access == .authorized || access == .limited else { throw Problem.noAccess }
        var created: String?
        try await PHPhotoLibrary.shared().performChanges {
            let request = PHAssetCreationRequest.forAsset()
            let options = PHAssetResourceCreationOptions()
            options.originalFilename = file.lastPathComponent
            options.shouldMoveFile = true
            request.addResource(with: video ? .video : .photo, fileURL: file, options: options)
            created = request.placeholderForCreatedAsset?.localIdentifier
        }
        if let created { await Backup.shared.cameFromPhotostead(created) }
    }
}

/// The system's "Save to Files": a copy of the file, wherever it's put.
struct SaveToFiles: UIViewControllerRepresentable {
    var file: URL
    var done: (Bool) -> Void

    func makeUIViewController(context: Context) -> UIDocumentPickerViewController {
        let picker = UIDocumentPickerViewController(forExporting: [file], asCopy: true)
        picker.delegate = context.coordinator
        return picker
    }

    func updateUIViewController(_ controller: UIDocumentPickerViewController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(done: done) }

    final class Coordinator: NSObject, UIDocumentPickerDelegate {
        let done: (Bool) -> Void
        init(done: @escaping (Bool) -> Void) { self.done = done }
        func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) { done(true) }
        func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) { done(false) }
    }
}
