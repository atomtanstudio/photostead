// What the phone sends of each item in its Photos library, and the names Photostead knows them by.
// The app and its background upload extension (Uploads/) both use these.

import Photos

/// The items to back up, newest first: photos, and videos unless they're left out.
func backupFetch(videos: Bool) -> PHFetchResult<PHAsset> {
    let options = PHFetchOptions()
    options.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: false)]
    options.predicate = videos ? NSPredicate(format: "mediaType == %d || mediaType == %d", PHAssetMediaType.image.rawValue, PHAssetMediaType.video.rawValue)
                               : NSPredicate(format: "mediaType == %d", PHAssetMediaType.image.rawValue)
    return PHAsset.fetchAssets(with: options)
}

/// An item's files to send: its original photo or video and a Live Photo's clip (which Photostead
/// pairs again), not a photo's edits; the full-size one only when there's no original.
func backupResources(for asset: PHAsset) -> [PHAssetResource] {
    let resources = PHAssetResource.assetResources(for: asset)
    return resources.filter { r in
        switch r.type {
        case .photo, .video, .pairedVideo: return true
        case .fullSizePhoto, .fullSizeVideo: return !resources.contains { $0.type == .photo || $0.type == .video }
        default: return false
        }
    }
}

/// The phone's name for one of an item's files ("<Photos item>|photo"), sent with it so Photostead
/// can say later which it has. The item's own id alone stands for all of its files.
func deviceItem(_ asset: String, _ type: PHAssetResourceType) -> String {
    let kind: String
    switch type {
    case .photo: kind = "photo"
    case .video: kind = "video"
    case .pairedVideo: kind = "pairedVideo"
    case .fullSizePhoto: kind = "fullSizePhoto"
    case .fullSizeVideo: kind = "fullSizeVideo"
    default: kind = String(type.rawValue)
    }
    return "\(asset)|\(kind)"
}

/// What comes with an upload (the server's UploadMeta).
struct UploadMeta: Codable {
    var fileName: String
    var takenAt: Date?
    var sha256: String? // checked once it's all there; unknown to iOS's own uploads
    var deviceItem: String?

    /// As the Photostead-Meta header: base64 JSON.
    var header: String {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return (try? encoder.encode(self).base64EncodedString()) ?? ""
    }
}

/// How the app is set to back up, for the extension (kept with the pairing, in the Keychain).
struct BackupOptions: Codable {
    var videos = true

    static func load() -> BackupOptions {
        Keychain.read("options").flatMap { try? JSONDecoder().decode(BackupOptions.self, from: $0) } ?? BackupOptions()
    }

    func save() { Keychain.write("options", try? JSONEncoder().encode(self)) }
}

/// What Photostead says it has of a phone's items.
struct DeviceCheck: Codable {
    var items: [String]
}

/// The phone's items Photostead has (from before uploads named them), with their files' hashes.
struct DeviceItems: Codable {
    struct Item: Codable { var item: String; var sha256: [String] }
    struct Recorded: Codable { var recorded: Int }
    var items: [Item]
}

struct DeviceKnown: Codable {
    var known: [String]
    var sending: [String]? // on their way now (from the app, or iOS): left to it
}
