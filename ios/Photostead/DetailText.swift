// An item's details in words, as the Details sheet shows them: the camera and lens, the exposure,
// the file, where and when. Kept apart from the views so they can be tested.

import Foundation

enum DetailText {
    /// The aperture as photographers write it: ƒ/1.78, ƒ/8.
    static func aperture(_ f: Double, locale: Locale = .current) -> String {
        "ƒ/" + f.formatted(.number.precision(.fractionLength(0...2)).locale(locale))
    }

    /// The shutter speed: a fraction of a second when it's a short one (1/250 s), otherwise seconds
    /// (0.6 s, 2 s, 30 s). A time that isn't near a whole fraction stays in seconds, as cameras show it.
    static func shutter(_ s: Double, locale: Locale = .current) -> String? {
        guard s > 0, s.isFinite else { return nil }
        if s < 1 {
            let d = 1 / s
            if abs(d - d.rounded()) <= d * 0.05 { return "1/\(Int(d.rounded())) s" }
        }
        return s.formatted(.number.precision(.fractionLength(0...1)).locale(locale)) + " s"
    }

    /// The focal length, with a tenth of a millimetre for a phone's short lenses (6.8 mm), whole
    /// above ten (50 mm).
    static func focal(_ mm: Double, locale: Locale = .current) -> String? {
        guard mm > 0, mm.isFinite else { return nil }
        return mm.formatted(.number.precision(.fractionLength(mm < 10 ? 0...1 : 0...0)).locale(locale)) + " mm"
    }

    /// Aperture, shutter, focal length and ISO, in the web app's order, leaving out what isn't known.
    static func exposure(_ c: ItemDetails.Camera, locale: Locale = .current) -> [String] {
        [c.fNumber.flatMap { $0 > 0 ? aperture($0, locale: locale) : nil }, c.exposureS.flatMap { shutter($0, locale: locale) },
         c.focalMm.flatMap { focal($0, locale: locale) }, c.iso.flatMap { $0 > 0 ? "ISO \($0)" : nil }]
            .compactMap { $0 }
    }

    /// The camera's name: its model, with the maker in front unless the model already says it
    /// ("Canon EOS R5", not "Canon Canon EOS R5").
    static func camera(make: String?, model: String?) -> String? {
        let make = make?.trimmingCharacters(in: .whitespaces) ?? "", model = model?.trimmingCharacters(in: .whitespaces) ?? ""
        if model.isEmpty { return make.isEmpty ? nil : make }
        if make.isEmpty || model.lowercased().hasPrefix(make.lowercased()) { return model }
        return make + " " + model
    }

    /// The lens, without the camera's name when it starts with it, as phones write theirs
    /// ("iPhone 15 Pro back triple camera 6.765mm f/1.78" is "Back triple camera 6.765mm f/1.78").
    static func lens(_ lens: String, model: String?) -> String {
        var l = lens.trimmingCharacters(in: .whitespaces)
        if let model = model?.trimmingCharacters(in: .whitespaces), !model.isEmpty, l.count > model.count, l.lowercased().hasPrefix(model.lowercased()) {
            l = String(l.dropFirst(model.count)).trimmingCharacters(in: .whitespaces)
            if let first = l.first { l = first.uppercased() + l.dropFirst() }
        }
        return l
    }

    /// Its size: megapixels (a photo's), pixels across and down, how long (a video), and the file.
    static func size(width: Int, height: Int, video: Bool, durationMs: Int?, bytes: Int64?, locale: Locale = .current) -> String {
        var parts: [String] = []
        if width > 0 && height > 0 {
            let mp = Double(width * height) / 1_000_000
            if !video && mp >= 0.1 { parts.append(mp.formatted(.number.precision(.fractionLength(mp < 10 ? 0...1 : 0...0)).locale(locale)) + " MP") }
            parts.append("\(width) × \(height)")
        }
        if video, let ms = durationMs, ms > 0 { parts.append(duration(ms)) }
        if let bytes, bytes > 0 { parts.append(ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file)) }
        return together(parts)
    }

    /// Parts joined with dots, each kept on one line when large text wraps them ("6.8 mm", not
    /// "6.8" and "mm" a line apart).
    static func together(_ parts: [String]) -> String {
        parts.map { $0.replacingOccurrences(of: " ", with: "\u{00A0}") }.joined(separator: " · ")
    }

    /// The kind of file, short, from its name: JPEG, HEIF, PNG, RAW, MOV…
    static func format(fileName: String) -> String? {
        let ext = (fileName as NSString).pathExtension.lowercased()
        switch ext {
        case "": return nil
        case "jpg", "jpeg": return "JPEG"
        case "heic", "heif": return "HEIF"
        case "webp": return "WebP"
        case "dng", "cr2", "cr3", "crw", "nef", "nrw", "arw", "srf", "sr2", "orf", "rw2", "raf", "pef", "srw", "3fr", "erf", "kdc", "mrw", "x3f", "iiq", "raw":
            return "RAW"
        default: return ext.uppercased()
        }
    }

    /// Where, in degrees: 47.6295° N, 122.3599° W.
    static func coordinates(lat: Double, lon: Double, locale: Locale = .current) -> String {
        let f = FloatingPointFormatStyle<Double>(locale: locale).precision(.fractionLength(4))
        return "\(abs(lat).formatted(f))° \(lat < 0 ? "S" : "N"), \(abs(lon).formatted(f))° \(lon < 0 ? "W" : "E")"
    }

    /// The time zone it was taken in, when the file says (otherwise the phone's).
    static func zone(offsetMin: Int?, fallback: TimeZone = .current) -> TimeZone {
        offsetMin.flatMap { TimeZone(secondsFromGMT: $0 * 60) } ?? fallback
    }

    /// The day it was taken, there: "Wednesday, November 12, 2026".
    static func day(_ date: Date, offsetMin: Int?, phone: TimeZone = .current, locale: Locale = .current) -> String {
        let f = DateFormatter()
        f.locale = locale
        f.timeZone = zone(offsetMin: offsetMin, fallback: phone)
        f.dateStyle = .full
        f.timeStyle = .none
        return f.string(from: date)
    }

    /// The time it was taken, on the clock where it was taken; with that place's offset when it isn't
    /// the phone's ("5:05 PM · UTC+9"), as the web app shows it.
    static func time(_ date: Date, offsetMin: Int?, phone: TimeZone = .current, locale: Locale = .current) -> String {
        let f = DateFormatter()
        f.locale = locale
        f.timeZone = zone(offsetMin: offsetMin, fallback: phone)
        f.dateStyle = .none
        f.timeStyle = .short
        let clock = f.string(from: date)
        guard let offsetMin, offsetMin * 60 != phone.secondsFromGMT(for: date) else { return clock }
        let h = abs(offsetMin) / 60, m = abs(offsetMin) % 60
        return clock + " · UTC" + (offsetMin < 0 ? "−" : "+") + "\(h)" + (m > 0 ? String(format: ":%02d", m) : "")
    }

    /// Where the word that it was made with AI came from, as the web app says it.
    static func evidence(_ g: ItemDetails.Generation) -> String {
        let file = g.evidence?.hasPrefix("video:") == true ? "video" : "file"
        var said: String
        switch g.source {
        case "comfyui": said = "From the ComfyUI workflow saved in the \(file)."
        case "a1111": said = "From the generation settings saved in the \(file)."
        case "midjourney": said = "From Midjourney’s prompt and job ID in the \(file)."
        case "grok": said = "From Grok Imagine’s signature in the \(file)."
        case "aigc": said = "From the AI-generated content label in the \(file)."
        case "c2pa": said = "From the \(file)’s Content Credentials (C2PA)" + (g.tool.map { ", made by \($0)." } ?? ".")
        case "iptc":
            said = g.evidence == "xmp:credit" ? "From the \(file)’s label: \(g.kind == "edited" ? "edited" : "made") with \(g.tool ?? "AI")."
                                               : "From the \(file)’s IPTC label: made with a generative model."
        default: said = "From the \(file)."
        }
        if g.incomplete == true { said += g.prompt == nil ? " The prompt and settings are still to be read." : " The rest is still to be read." }
        return said
    }

    /// A word on the date when it's a guess (no date inside the file) or was set by hand; nothing
    /// for a camera's own time.
    static func dateNote(_ source: String?) -> String? {
        switch source {
        case "file-modified": "The file’s date: there’s no date inside it"
        case "name": "From its name: there’s no date inside it"
        case "user": "Set by you"
        default: nil
        }
    }
}
