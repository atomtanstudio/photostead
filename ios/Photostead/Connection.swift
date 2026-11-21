// The connection to a Photostead server: pairing with a code (from the QR code Photostead shows
// under Devices & remote access), the pairing and the device's token (in the Keychain, shared with
// the background upload extension), and finding which of the server's addresses answers: its home
// address at home; away, its tailnet address, through Tailscale or, with no Tailscale on the phone,
// through Funnel, where Photostead lets paired devices in with their own token.

import Foundation
import Security

/// What pairing keeps: whose server, where it can be found, and what this phone is called there.
struct Pairing: Codable, Equatable {
    var serverID: String
    var serverName: String
    var addresses: [URL]
    var lastAnswered: URL?
    var deviceName: String
    var account: String
    var addressesChecked: Date? // when the server last said where it can be found

    /// The addresses to try: the one that answered last, then the rest (tailnet, public, home).
    var candidates: [URL] {
        var seen = Set<URL>()
        return ([lastAnswered].compactMap { $0 } + addresses).filter { seen.insert($0).inserted }
    }

    private static let key = "pairing"

    static func load() -> Pairing? {
        if let data = Keychain.read(key) { return try? JSONDecoder().decode(Pairing.self, from: data) }
        // Kept in the app's own settings before the extension needed it: moved, with the token.
        guard let data = UserDefaults.standard.data(forKey: key), let pairing = try? JSONDecoder().decode(Pairing.self, from: data) else { return nil }
        pairing.save()
        guard Keychain.read(key) != nil else { return pairing } // the Keychain said no: kept where it was, tried again next time
        if let token = Keychain.token { Keychain.token = token } // written again: into the shared group
        UserDefaults.standard.removeObject(forKey: key)
        return pairing
    }

    func save() {
        Keychain.write(Pairing.key, try? JSONEncoder().encode(self))
    }

    static func forget() {
        UserDefaults.standard.removeObject(forKey: key)
        Keychain.write(key, nil)
        Keychain.token = nil
    }
}

/// A pairing link (photostead://pair?code=…&server=…&url=…&url=…), or a code typed by hand.
struct PairingLink: Equatable {
    var code: String
    var serverID = ""
    var urls: [URL] = []

    init?(_ text: String) {
        let value = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if let link = URLComponents(string: value), link.scheme?.lowercased() == "photostead", link.host == "pair" {
            let items = link.queryItems ?? []
            code = items.first { $0.name == "code" }?.value ?? ""
            serverID = items.first { $0.name == "server" }?.value ?? ""
            urls = items.filter { $0.name == "url" }.compactMap { $0.value.flatMap(serverURL) }
        } else {
            code = value
        }
        if code.count < 8 { return nil }
    }
}

/// A server address as typed: a scheme (http for a bare IP or .local name, else https), no path.
func serverURL(_ text: String) -> URL? {
    var value = text.trimmingCharacters(in: .whitespacesAndNewlines)
    if value.isEmpty { return nil }
    if !value.contains("://") {
        let host = value.split(separator: ":").first.map(String.init) ?? value
        let local = host.allSatisfy { $0.isNumber || $0 == "." } || host.hasSuffix(".local") || host == "localhost"
        value = (local ? "http://" : "https://") + value
    }
    guard var parts = URLComponents(string: value), let scheme = parts.scheme?.lowercased(), ["http", "https"].contains(scheme), parts.host != nil else { return nil }
    parts.scheme = scheme
    parts.path = ""
    parts.query = nil
    parts.fragment = nil
    return parts.url
}

/// The most Photostead takes in one request, from an Upload-Limit header's max-append-size (it asks
/// for pieces through Cloudflare, which turns bigger requests away): nil when a file may go whole.
func pieceSize(_ uploadLimit: String?) -> Int64? {
    for part in (uploadLimit ?? "").split(separator: ",") {
        let kv = part.split(separator: "=", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
        if kv.count == 2, kv[0] == "max-append-size", let n = Int64(kv[1]), n > 0 { return n }
    }
    return nil
}

/// An address on the home network, as Photostead tells them apart: a private IPv4 address, or a
/// .local, .lan or .home.arpa name.
func isHomeAddress(_ url: URL) -> Bool {
    guard let host = url.host()?.lowercased() else { return false }
    if host == "localhost" || host.hasSuffix(".local") || host.hasSuffix(".lan") || host.hasSuffix(".home.arpa") { return true }
    let parts = host.split(separator: ".").compactMap { UInt8($0) }
    guard parts.count == 4 else { return false }
    return parts[0] == 10 || (parts[0] == 172 && (16...31).contains(parts[1])) || (parts[0] == 192 && parts[1] == 168)
}

/// An address as people read it: its host, and its port when it isn't the usual one.
func displayAddress(_ url: URL) -> String {
    guard let host = url.host() else { return url.absoluteString }
    return url.port.map { "\(host):\($0)" } ?? host
}

/// The Keychain items the app and its background upload extension share: written to the first
/// group in keychain-access-groups (…studio.atomtan.photostead.shared, in both), readable after the first
/// unlock so a backup in the background can use them.
enum Keychain {
    private static let service = "studio.atomtan.photostead"

    /// The paired device's token (psd_…).
    static var token: String? {
        get { read("device-token").flatMap { String(data: $0, encoding: .utf8) } }
        set { write("device-token", newValue.map { Data($0.utf8) }) }
    }

    static func read(_ account: String) -> Data? {
        var item: CFTypeRef?
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                    kSecAttrAccount as String: account, kSecReturnData as String: true]
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess else { return nil }
        return item as? Data
    }

    /// Replaces the item (in whichever group it was), or removes it.
    static func write(_ account: String, _ data: Data?) {
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account]
        SecItemDelete(query as CFDictionary)
        guard let data else { return }
        var add = query
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(add as CFDictionary, nil)
    }
}

enum PhotosteadError: LocalizedError {
    case unreachable([URL], why: [String] = []) // why: each address's reason, in the same order
    case refused(Int, String)
    case notPaired

    var errorDescription: String? {
        switch self {
        case .unreachable(let urls, let why):
            let list = urls.enumerated().map { i, url in
                let host = url.host() ?? url.absoluteString
                return i < why.count && !why[i].isEmpty ? "\(host) (\(why[i]))" : host
            }.joined(separator: " or ")
            return urls.isEmpty ? "Photostead's address isn't known." : "Photostead didn't answer at \(list)."
        case .refused(let status, let detail):
            return detail.isEmpty ? "Photostead said no (\(status))." : detail
        case .notPaired:
            return "Pair with Photostead first."
        }
    }
}

/// Photostead's API, at whichever of its addresses answers.
actor Server {
    private(set) var pairing: Pairing
    private let token: String
    private var reached: (url: URL, at: Date)?
    let session: URLSession

    init(pairing: Pairing, token: String) {
        self.pairing = pairing
        self.token = token
        let config = URLSessionConfiguration.default
        config.waitsForConnectivity = false
        config.timeoutIntervalForRequest = 60
        session = URLSession(configuration: config)
    }

    /// The address that answers now as the same server, remembered for a few minutes. See
    /// `answering`: the home address whenever it answers.
    func base() async throws -> URL {
        if let r = reached, Date().timeIntervalSince(r.at) < 300 { return r.url }
        let list = pairing.candidates
        let found = await Server.answering(list, serverID: pairing.serverID, session: session)
        guard let url = found.url else {
            throw PhotosteadError.unreachable(list, why: list.map { found.why[$0] ?? "" })
        }
        reached = (url, Date())
        if pairing.lastAnswered != url {
            pairing.lastAnswered = url
            pairing.save()
        }
        if pairing.addressesChecked.map({ Date().timeIntervalSince($0) > 24 * 3600 }) ?? true {
            await learnAddresses(at: url)
        }
        return url
    }

    /// The most Photostead takes in one request at this address (see pieceSize), or nil.
    func pieceLimit(at base: URL) async -> Int64? {
        var request = URLRequest(url: base.appending(path: "api/v1/uploads/resumable"))
        request.httpMethod = "OPTIONS"
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.timeoutInterval = 15
        guard let (_, response) = try? await session.data(for: request), let http = response as? HTTPURLResponse else { return nil }
        return pieceSize(http.value(forHTTPHeaderField: "Upload-Limit"))
    }

    /// Where the server says it can be found now, at most daily: an address it learned after this
    /// phone paired (its home address, say, or Tailscale turned on) is tried from then on.
    private func learnAddresses(at url: URL) async {
        var request = URLRequest(url: url.appending(path: "api/v1/server/addresses"))
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.timeoutInterval = 15
        guard let (data, response) = try? await session.data(for: request), (response as? HTTPURLResponse)?.statusCode == 200,
              let a = try? JSONDecoder().decode(Paired.Addresses.self, from: data) else { return }
        let addresses = [a.tailnet, a.public, a.home].compactMap { $0.flatMap(serverURL) }
        if !addresses.isEmpty { pairing.addresses = addresses }
        pairing.addressesChecked = Date()
        pairing.save()
    }

    private enum Probe: Sendable {
        case answered(URL), failed(URL, String), graceOver
    }

    /// Which address answers as this server, all asked at once (home, when away, would otherwise
    /// hang). The home address wins whenever it answers: the tailnet address answers at home too
    /// (through Funnel, without Tailscale), but backups would go out to the internet and back. Away,
    /// the first other address that answers, after a second's grace for home. The home address
    /// gets a few seconds (away, it hangs); the others longer, as cellular and Funnel can be slow to
    /// start. When none answers, `why` says, for each address, why not.
    static func answering(_ list: [URL], serverID: String, session: URLSession) async -> (url: URL?, why: [URL: String]) {
        await withTaskGroup(of: Probe.self) { group in
            for url in list {
                let timeout: TimeInterval = list.count == 1 ? 15 : isHomeAddress(url) ? 4 : 12
                group.addTask {
                    let why = await whyNot(url, serverID: serverID, session: session, timeout: timeout)
                    return why.map { .failed(url, $0) } ?? .answered(url)
                }
            }
            var homeLeft = list.filter(isHomeAddress).count
            var away: URL?
            var why: [URL: String] = [:]
            for await probe in group {
                switch probe {
                case .answered(let url) where isHomeAddress(url):
                    group.cancelAll()
                    return (url, why)
                case .answered(let url):
                    guard away == nil else { continue }
                    away = url
                    if homeLeft == 0 {
                        group.cancelAll()
                        return (url, why)
                    }
                    group.addTask {
                        try? await Task.sleep(for: .seconds(1))
                        return .graceOver
                    }
                case .failed(let url, let reason):
                    why[url] = reason
                    if isHomeAddress(url) { homeLeft -= 1 }
                    if homeLeft == 0, let away {
                        group.cancelAll()
                        return (away, why)
                    }
                case .graceOver:
                    group.cancelAll()
                    return (away, why)
                }
            }
            return (away, why)
        }
    }

    /// Whether Photostead answers at this address as the server this phone is paired with: nil
    /// when it does, else why not, in a few words.
    private static func whyNot(_ url: URL, serverID: String, session: URLSession, timeout: TimeInterval) async -> String? {
        var request = URLRequest(url: url.appending(path: "api/v1/server"))
        request.timeoutInterval = timeout
        let data: Data, response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            return reason(error)
        }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else { return "answered \(status)" }
        guard let info = try? JSONDecoder().decode(ServerInfo.self, from: data) else { return "not Photostead" }
        return serverID.isEmpty || info.id.lowercased() == serverID.lowercased() ? nil : "another Photostead"
    }

    /// Why a request didn't get through, in a few words.
    static func reason(_ error: Error) -> String {
        guard let e = error as? URLError else { return error.localizedDescription }
        switch e.code {
        case .notConnectedToInternet:
            switch e.networkUnavailableReason {
            case .cellular: return "cellular data is off for Photostead"
            case .constrained: return "Low Data Mode"
            case .expensive: return "cellular data not allowed"
            default: return "no connection"
            }
        case .timedOut: return "timed out"
        case .cannotFindHost, .dnsLookupFailed: return "name not found"
        case .cannotConnectToHost: return "refused"
        case .networkConnectionLost: return "connection dropped"
        case .secureConnectionFailed, .serverCertificateUntrusted, .serverCertificateHasBadDate, .serverCertificateNotYetValid,
             .serverCertificateHasUnknownRoot: return "secure connection failed"
        case .cancelled: return "cancelled"
        default: return "error \(e.code.rawValue)"
        }
    }

    func forgetReached() { reached = nil }

    /// A request to the API, signed with the device's token.
    /// A path on Photostead, with its query kept as a query: appending(path:) alone would turn the
    /// "?" into %3F, and Photostead would answer 404.
    nonisolated static func url(_ base: URL, _ path: String) -> URL {
        let parts = path.split(separator: "?", maxSplits: 1)
        var url = base.appending(path: String(parts[0]))
        if parts.count == 2, var c = URLComponents(url: url, resolvingAgainstBaseURL: false) {
            c.percentEncodedQuery = String(parts[1])
            url = c.url ?? url
        }
        return url
    }

    /// A request to a path on Photostead, which may end in a query ("api/v1/search?q=…", its values
    /// already encoded with `query(_:)`).
    func request(_ method: String, _ path: String, headers: [String: String] = [:]) async throws -> URLRequest {
        var request = URLRequest(url: Server.url(try await base(), path))
        request.httpMethod = method
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        for (k, v) in headers { request.setValue(v, forHTTPHeaderField: k) }
        return request
    }

    /// Sends JSON, returns JSON.
    func call<T: Decodable>(_ method: String, _ path: String, body: (some Encodable)? = Optional<String>.none, as type: T.Type) async throws -> T {
        var request = try await request(method, path, headers: ["Content-Type": "application/json"])
        if let body { request.httpBody = try JSONEncoder().encode(body) }
        let (data, response): (Data, URLResponse)
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            reached = nil // went away mid-way: look again next time
            throw error
        }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw PhotosteadError.refused(status, problemDetail(data)) }
        return try JSONDecoder.photostead.decode(T.self, from: data)
    }

    /// Pairs with a code at the first address that answers: the device's token, and the server's
    /// id and addresses to keep.
    static func pair(urls: [URL], code: String, name: String) async throws -> (Pairing, String) {
        var last: Error = PhotosteadError.unreachable(urls)
        for url in urls {
            var request = URLRequest(url: url.appending(path: "api/v1/devices/pair"))
            request.httpMethod = "POST"
            request.timeoutInterval = 15
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONEncoder().encode(["code": code.trimmingCharacters(in: .whitespaces), "name": name, "platform": "ios"])
            do {
                let (data, response) = try await URLSession.shared.data(for: request)
                let status = (response as? HTTPURLResponse)?.statusCode ?? 0
                if status == 404 { // not Photostead's own door here (Funnel answers phones that way): the next address
                    last = PhotosteadError.unreachable([url], why: ["pairing works at home or over Tailscale"])
                    continue
                }
                guard status == 200 else { throw PhotosteadError.refused(status, problemDetail(data)) }
                let paired = try JSONDecoder.photostead.decode(Paired.self, from: data)
                let a = paired.addresses
                let addresses = [a.tailnet, a.public, a.home].compactMap { $0.flatMap(serverURL) }
                let pairing = Pairing(serverID: paired.server.id, serverName: paired.server.name, addresses: addresses.isEmpty ? [url] : addresses,
                                      lastAnswered: url, deviceName: name, account: paired.user.displayName.isEmpty ? paired.user.email : paired.user.displayName)
                return (pairing, paired.token)
            } catch let error as PhotosteadError {
                throw error // it answered: a wrong code isn't an address problem
            } catch {
                last = PhotosteadError.unreachable([url], why: [reason(error)])
            }
        }
        throw last
    }
}

struct ServerInfo: Decodable {
    var id: String
    var name: String
}

private struct Paired: Decodable {
    struct User: Decodable {
        var email: String
        var displayName: String
    }
    struct Addresses: Decodable {
        var home: String?
        var tailnet: String?
        var `public`: String?
    }
    var token: String
    var user: User
    var server: ServerInfo
    var addresses: Addresses
}

/// The detail of an error reply ({"detail": …} or a problem's title).
func problemDetail(_ data: Data) -> String {
    guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return "" }
    return (object["detail"] as? String) ?? (object["title"] as? String) ?? ""
}

extension JSONDecoder {
    /// Photostead's JSON: dates as Go writes them (RFC 3339, with fractional seconds or without,
    /// which .iso8601 alone refuses).
    static let photostead: JSONDecoder = {
        let d = JSONDecoder()
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let whole = ISO8601DateFormatter()
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            if let date = fractional.date(from: s) ?? whole.date(from: s) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "Not a date: \(s)"))
        }
        return d
    }()
}
