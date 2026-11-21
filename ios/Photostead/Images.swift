// Pictures from Photostead: thumbnails and previews (WebP), with the device's token, cached in
// memory and on disk; and while one loads, its ThumbHash (a few bytes from the sync feed) drawn as
// a blurred placeholder, as the web app does.

import SwiftUI
import UIKit

final class Images: @unchecked Sendable {
    static let shared = Images()

    private let memory = NSCache<NSString, UIImage>()
    private let placeholders = NSCache<NSString, UIImage>()
    private let session: URLSession

    private init() {
        memory.totalCostLimit = 160 << 20
        let config = URLSessionConfiguration.default
        config.urlCache = URLCache(memoryCapacity: 32 << 20, diskCapacity: 1 << 30, directory: FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0].appending(path: "pictures"))
        config.requestCachePolicy = .returnCacheDataElseLoad // a picture of an item doesn't change
        config.httpMaximumConnectionsPerHost = 6
        session = URLSession(configuration: config)
    }

    func cached(_ id: String, _ what: String, version: String = "") -> UIImage? { memory.object(forKey: "\(what)/\(id)\(version)" as NSString) }

    /// An item's thumbnail or preview, decoded off the main thread. version (from its ThumbHash)
    /// is new when the picture is (an edited photo), so the cached copy of the old one isn't used.
    func image(_ id: String, _ what: String, version: String = "", base: URL, token: String) async -> UIImage? {
        await picture(path: "api/v1/assets/\(id)/\(what)", query: version.isEmpty ? nil : version, key: "\(what)/\(id)\(version)", base: base, token: token)
    }

    /// An item's picture version, from its ThumbHash.
    static func version(_ hash: Data?) -> String {
        guard let hash, !hash.isEmpty else { return "" }
        var h: UInt32 = 5381
        for b in hash { h = (h &* 33) ^ UInt32(b) }
        return "?v=" + String(h, radix: 36)
    }

    /// A picture from the server (an item's, a face's), cached under key.
    func picture(path: String, query: String? = nil, key k: String, base: URL, token: String) async -> UIImage? {
        let key = k as NSString
        if let image = memory.object(forKey: key) { return image }
        var url = base.appending(path: path)
        if let query, let v = query.split(separator: "=").last { url.append(queryItems: [URLQueryItem(name: "v", value: String(v))]) }
        var request = URLRequest(url: url)
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        guard let (data, response) = try? await session.data(for: request), (response as? HTTPURLResponse)?.statusCode == 200,
              let image = await UIImage(data: data)?.byPreparingForDisplay() else { return nil }
        memory.setObject(image, forKey: key, cost: Int(image.size.width * image.size.height * image.scale * image.scale * 4))
        return image
    }

    /// The blurred placeholder for a ThumbHash.
    func placeholder(_ hash: Data?) -> UIImage? {
        guard let hash, hash.count >= 5 else { return nil }
        let key = hash.base64EncodedString() as NSString
        if let image = placeholders.object(forKey: key) { return image }
        guard let image = ThumbHash.image(hash) else { return nil }
        placeholders.setObject(image, forKey: key)
        return image
    }
}

/// ThumbHash (https://evanw.github.io/thumbhash/): a tiny image placeholder, decoded to pixels.
enum ThumbHash {
    static func image(_ hash: Data) -> UIImage? {
        guard case (let w, let h, var rgba)? = decode([UInt8](hash)) else { return nil }
        for i in stride(from: 0, to: rgba.count, by: 4) where rgba[i + 3] < 255 { // CGImage wants it premultiplied
            let a = Double(rgba[i + 3]) / 255
            for c in 0..<3 { rgba[i + c] = UInt8(Double(rgba[i + c]) * a) }
        }
        let provider = CGDataProvider(data: Data(rgba) as CFData)!
        guard let cg = CGImage(width: w, height: h, bitsPerComponent: 8, bitsPerPixel: 32, bytesPerRow: w * 4, space: CGColorSpaceCreateDeviceRGB(),
                               bitmapInfo: CGBitmapInfo(rawValue: CGImageAlphaInfo.premultipliedLast.rawValue), provider: provider,
                               decode: nil, shouldInterpolate: true, intent: .defaultIntent) else { return nil }
        return UIImage(cgImage: cg)
    }

    static func aspectRatio(_ hash: [UInt8]) -> Double {
        let header = hash[3]
        let hasAlpha = hash[2] & 0x80 != 0
        let isLandscape = hash[4] & 0x80 != 0
        let lx = isLandscape ? (hasAlpha ? 5 : 7) : Int(header & 7)
        let ly = isLandscape ? Int(header & 7) : (hasAlpha ? 5 : 7)
        return Double(lx) / Double(ly)
    }

    static func decode(_ hash: [UInt8]) -> (Int, Int, [UInt8])? {
        guard hash.count >= 5 else { return nil }
        let header24 = Int(hash[0]) | Int(hash[1]) << 8 | Int(hash[2]) << 16
        let header16 = Int(hash[3]) | Int(hash[4]) << 8
        let lDC = Double(header24 & 63) / 63
        let pDC = Double((header24 >> 6) & 63) / 31.5 - 1
        let qDC = Double((header24 >> 12) & 63) / 31.5 - 1
        let lScale = Double((header24 >> 18) & 31) / 31
        let hasAlpha = (header24 >> 23) != 0
        let pScale = Double((header16 >> 3) & 63) / 63
        let qScale = Double((header16 >> 9) & 63) / 63
        let isLandscape = (header16 >> 15) != 0
        let lx = max(3, isLandscape ? (hasAlpha ? 5 : 7) : header16 & 7)
        let ly = max(3, isLandscape ? header16 & 7 : (hasAlpha ? 5 : 7))
        let aDC = hasAlpha && hash.count > 5 ? Double(hash[5] & 15) / 15 : 1
        let aScale = hasAlpha && hash.count > 5 ? Double(hash[5] >> 4) / 15 : 0

        let acStart = hasAlpha ? 6 : 5
        var acIndex = 0
        func channel(_ nx: Int, _ ny: Int, _ scale: Double) -> [Double] {
            var ac: [Double] = []
            for cy in 0..<ny {
                var cx = cy > 0 ? 0 : 1
                while cx * ny < nx * (ny - cy) {
                    let byte = acStart + (acIndex >> 1)
                    let nibble = byte < hash.count ? (Int(hash[byte]) >> ((acIndex & 1) << 2)) & 15 : 7
                    ac.append((Double(nibble) / 7.5 - 1) * scale)
                    acIndex += 1
                    cx += 1
                }
            }
            return ac
        }
        let lAC = channel(lx, ly, lScale)
        let pAC = channel(3, 3, pScale * 1.25)
        let qAC = channel(3, 3, qScale * 1.25)
        let aAC = hasAlpha ? channel(5, 5, aScale) : []

        let ratio = aspectRatio(hash)
        let w = Int((ratio > 1 ? 32 : 32 * ratio).rounded())
        let h = Int((ratio > 1 ? 32 / ratio : 32).rounded())
        guard w > 0, h > 0 else { return nil }
        var rgba = [UInt8](repeating: 0, count: w * h * 4)
        let nx = max(lx, hasAlpha ? 5 : 3), ny = max(ly, hasAlpha ? 5 : 3)
        var fx = [Double](repeating: 0, count: nx), fy = [Double](repeating: 0, count: ny)
        var i = 0
        for y in 0..<h {
            for x in 0..<w {
                var l = lDC, p = pDC, q = qDC, a = aDC
                for cx in 0..<nx { fx[cx] = cos(Double.pi / Double(w) * (Double(x) + 0.5) * Double(cx)) }
                for cy in 0..<ny { fy[cy] = cos(Double.pi / Double(h) * (Double(y) + 0.5) * Double(cy)) }
                var j = 0
                for cy in 0..<ly {
                    var cx = cy > 0 ? 0 : 1
                    let fy2 = fy[cy] * 2
                    while cx * ly < lx * (ly - cy) {
                        l += lAC[j] * fx[cx] * fy2
                        j += 1
                        cx += 1
                    }
                }
                j = 0
                for cy in 0..<3 {
                    var cx = cy > 0 ? 0 : 1
                    let fy2 = fy[cy] * 2
                    while cx < 3 - cy {
                        let f = fx[cx] * fy2
                        p += pAC[j] * f
                        q += qAC[j] * f
                        j += 1
                        cx += 1
                    }
                }
                if hasAlpha {
                    j = 0
                    for cy in 0..<5 {
                        var cx = cy > 0 ? 0 : 1
                        let fy2 = fy[cy] * 2
                        while cx < 5 - cy {
                            a += aAC[j] * fx[cx] * fy2
                            j += 1
                            cx += 1
                        }
                    }
                }
                let b = l - 2 / 3 * p
                let r = (3 * l - b + q) / 2
                let g = r - q
                rgba[i] = UInt8(max(0, 255 * min(1, r)))
                rgba[i + 1] = UInt8(max(0, 255 * min(1, g)))
                rgba[i + 2] = UInt8(max(0, 255 * min(1, b)))
                rgba[i + 3] = UInt8(max(0, 255 * min(1, a)))
                i += 4
            }
        }
        return (w, h, rgba)
    }
}

/// An item's picture at a size: the placeholder, then the thumbnail (or preview) once it's here.
struct ItemPicture: View {
    var item: Item
    var what = "thumbnail"
    var contentMode: ContentMode = .fill
    @State private var image: UIImage?

    private var veiled: Bool { Library.shared.veiled(item) }

    var body: some View {
        ZStack {
            if let image {
                Image(uiImage: image).resizable().aspectRatio(contentMode: contentMode)
            } else if let placeholder = Images.shared.placeholder(item.thumbhash) {
                Image(uiImage: placeholder).resizable().aspectRatio(contentMode: contentMode)
            } else {
                Rectangle().fill(.quaternary)
            }
        }
        .blur(radius: veiled ? (what == "thumbnail" ? 14 : 48) : 0, opaque: true) // adult, for someone who blurs it
        .saturation(veiled ? 0.7 : 1)
        .task(id: item.id + what + Images.version(item.thumbhash) + (Library.shared.base?.absoluteString ?? "")) {
            let v = Images.version(item.thumbhash)
            image = Images.shared.cached(item.id, what, version: v)
            if image == nil, what == "preview" { image = Images.shared.cached(item.id, "thumbnail", version: v) } // sharper soon
            let library = Library.shared
            guard let base = library.base, let token = library.token else { return }
            if let got = await Images.shared.image(item.id, what, version: v, base: base, token: token) { image = got }
        }
    }
}
