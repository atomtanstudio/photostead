@testable import Photostead
import XCTest

final class PairingTests: XCTestCase {
    func testLinkWithAddresses() {
        let link = PairingLink("photostead://pair?code=AB12CD34&server=5c1e&url=https%3A%2F%2Fphotostead.example.ts.net&url=http%3A%2F%2F192.168.1.10%3A4800")
        XCTAssertEqual(link?.code, "AB12CD34")
        XCTAssertEqual(link?.serverID, "5c1e")
        XCTAssertEqual(link?.urls.map(\.absoluteString), ["https://photostead.example.ts.net", "http://192.168.1.10:4800"])
    }

    func testCodeByHand() {
        XCTAssertEqual(PairingLink("  ab12cd34 ")?.code, "ab12cd34")
        XCTAssertNil(PairingLink("short"))
        XCTAssertNil(PairingLink("photostead://pair?server=x"))
    }

    func testAddressesAsTyped() {
        XCTAssertEqual(serverURL("192.168.1.10:4800")?.absoluteString, "http://192.168.1.10:4800")
        XCTAssertEqual(serverURL("photostead.example.ts.net")?.absoluteString, "https://photostead.example.ts.net")
        XCTAssertEqual(serverURL("https://photos.example.com/some/page?x=1")?.absoluteString, "https://photos.example.com")
        XCTAssertEqual(serverURL("nas.local:4800")?.absoluteString, "http://nas.local:4800")
        XCTAssertNil(serverURL("ftp://x"))
        XCTAssertNil(serverURL(""))
    }

    func testHomeAddresses() {
        for home in ["http://192.168.1.10:4800", "http://10.0.0.5:4800", "http://172.20.1.2", "http://nas.local:4800", "http://photos.lan", "http://box.home.arpa"] {
            XCTAssertTrue(isHomeAddress(URL(string: home)!), home)
        }
        for away in ["https://photostead.example.ts.net", "http://100.101.102.103:4800", "https://photos.example.com", "http://172.32.0.1", "http://8.8.8.8"] {
            XCTAssertFalse(isHomeAddress(URL(string: away)!), away)
        }
    }

    /// Through Cloudflare, Photostead says how much it takes in one request.
    func testPieceSize() {
        XCTAssertEqual(pieceSize("max-size=53687091200, max-age=259200, max-append-size=67108864"), 67108864)
        XCTAssertNil(pieceSize("max-size=53687091200, max-age=259200"))
        XCTAssertNil(pieceSize(nil))
        XCTAssertNil(pieceSize("max-append-size=0"))
    }

    /// A piece of a file, or the rest of it, as a file of its own.
    func testSlices() throws {
        let dir = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let bytes = Data((0..<10_000_000).map { UInt8($0 % 251) }) // more than one read
        let whole = dir.appending(path: "whole")
        try bytes.write(to: whole)
        try slice(of: whole, from: 1_000, length: 6_000_000, to: dir.appending(path: "piece"))
        XCTAssertEqual(try Data(contentsOf: dir.appending(path: "piece")), bytes[1_000..<6_001_000])
        try slice(of: whole, from: 6_001_000, to: dir.appending(path: "rest"))
        XCTAssertEqual(try Data(contentsOf: dir.appending(path: "rest")), bytes[6_001_000...])
    }

    func testCandidatesLastAnsweredFirst() {
        let home = URL(string: "http://192.168.1.10:4800")!, tailnet = URL(string: "https://photostead.example.ts.net")!
        let p = Pairing(serverID: "x", serverName: "Photostead", addresses: [tailnet, home], lastAnswered: home, deviceName: "iPhone", account: "Rich")
        XCTAssertEqual(p.candidates, [home, tailnet])
    }
}

/// Which address the app uses: the home address whenever it answers (the tailnet address answers at
/// home too, through Funnel), else the first other one, after a second's grace for home.
final class AnsweringTests: XCTestCase {
    let home = URL(string: "http://192.168.1.10:4800")!, tailnet = URL(string: "https://photostead.example.ts.net")!

    override func tearDown() {
        StubServer.answers = [:]
    }

    func answering(_ list: [URL], serverID: String = "s1") async -> (url: URL?, why: [URL: String]) {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [StubServer.self]
        return await Server.answering(list, serverID: serverID, session: URLSession(configuration: config))
    }

    func pick(_ list: [URL]) async -> URL? {
        await answering(list).url
    }

    func testHomeWinsWhenItAnswers() async {
        StubServer.answers = ["photostead.example.ts.net": (0, "s1"), "192.168.1.10": (0.3, "s1")]
        let got = await pick([tailnet, home])
        XCTAssertEqual(got, home)
    }

    func testAwayAfterTheGrace() async {
        StubServer.answers = ["photostead.example.ts.net": (0, "s1")] // home never answers
        let start = Date()
        let got = await pick([home, tailnet])
        XCTAssertEqual(got, tailnet)
        XCTAssertLessThan(Date().timeIntervalSince(start), 2)
    }

    func testAwayAtOnceWhenHomeSaysNo() async {
        StubServer.answers = ["photostead.example.ts.net": (0, "s1"), "192.168.1.10": (0, nil)]
        let start = Date()
        let got = await pick([tailnet, home])
        XCTAssertEqual(got, tailnet)
        XCTAssertLessThan(Date().timeIntervalSince(start), 0.9)
    }

    func testAnotherServerIsntOurs() async {
        StubServer.answers = ["photostead.example.ts.net": (0, "someone-else"), "192.168.1.10": (0, nil)]
        let got = await pick([tailnet, home])
        XCTAssertNil(got)
    }

    /// When none answers, it says why for each address.
    func testWhyNot() async {
        StubServer.answers = ["photostead.example.ts.net": (0, "404"), "192.168.1.10": (0, nil)]
        let found = await answering([tailnet, home])
        XCTAssertNil(found.url)
        XCTAssertEqual(found.why, [tailnet: "answered 404", home: "refused"])
        let error = PhotosteadError.unreachable([tailnet, home], why: [tailnet, home].map { found.why[$0] ?? "" })
        XCTAssertEqual(error.localizedDescription, "Photostead didn't answer at photostead.example.ts.net (answered 404) or 192.168.1.10 (refused).")
        XCTAssertEqual(PhotosteadError.unreachable([home]).localizedDescription, "Photostead didn't answer at 192.168.1.10.")
        XCTAssertEqual(Server.reason(URLError(.timedOut)), "timed out")
    }
}

/// Answers GET /api/v1/server for each host: with an id after a delay (or 404 for "404"), at once
/// with an error (no id), or never (a host that isn't there).
final class StubServer: URLProtocol {
    static var answers: [String: (delay: TimeInterval, id: String?)] = [:]
    private var stopped = false

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        guard let url = request.url, let answer = url.host().flatMap({ Self.answers[$0] }) else { return }
        guard let id = answer.id else {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + answer.delay) { [self] in
            guard !stopped else { return }
            let status = id == "404" ? 404 : 200 // "404": answers, but not as Photostead
            let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(#"{"id":"\#(id)","name":"Photostead"}"#.utf8))
            client?.urlProtocolDidFinishLoading(self)
        }
    }

    override func stopLoading() {
        DispatchQueue.main.async { [self] in stopped = true }
    }
}

/// Pairings from before the upload extension were kept in the app's settings: moved to the Keychain
/// (where the extension reads it), with the token, on the first look.
final class KeychainTests: XCTestCase {
    func testPairingMovesToTheKeychain() throws {
        Pairing.forget()
        defer { Pairing.forget() }
        let pairing = Pairing(serverID: "s1", serverName: "Home", addresses: [URL(string: "https://photostead.example")!], deviceName: "iPhone", account: "Me")
        UserDefaults.standard.set(try JSONEncoder().encode(pairing), forKey: "pairing")
        Keychain.token = "psd_test"
        XCTAssertEqual(Pairing.load(), pairing)
        XCTAssertNil(UserDefaults.standard.data(forKey: "pairing"), "still in the settings")
        XCTAssertNotNil(Keychain.read("pairing"), "not in the Keychain")
        XCTAssertEqual(Keychain.token, "psd_test")
        XCTAssertEqual(Pairing.load(), pairing, "not found again")
    }
}

final class DecodingTests: XCTestCase {
    func testDatesAsGoWritesThem() throws {
        struct T: Decodable { var at: Date }
        let frac = try JSONDecoder.photostead.decode(T.self, from: Data(#"{"at":"2026-10-25T01:12:08.094652-07:00"}"#.utf8))
        let whole = try JSONDecoder.photostead.decode(T.self, from: Data(#"{"at":"2026-10-25T08:12:08Z"}"#.utf8))
        XCTAssertEqual(frac.at.timeIntervalSince1970, whole.at.timeIntervalSince1970, accuracy: 0.1)
    }
}

final class AddressTests: XCTestCase {
    func testDisplayAddress() {
        XCTAssertEqual(displayAddress(URL(string: "http://127.0.0.1:4810")!), "127.0.0.1:4810")
        XCTAssertEqual(displayAddress(URL(string: "https://photostead.example.ts.net")!), "photostead.example.ts.net")
    }

    func testQueriesStayQueries() {
        let base = URL(string: "http://127.0.0.1:4810")!
        XCTAssertEqual(Server.url(base, "api/v1/sync/stream?since=401313").absoluteString, "http://127.0.0.1:4810/api/v1/sync/stream?since=401313")
        XCTAssertEqual(Server.url(base, "api/v1/timeline?collection=\(query("person:1"))&month=\(query("2026-10"))").absoluteString,
                       "http://127.0.0.1:4810/api/v1/timeline?collection=person%3A1&month=2026-10")
        XCTAssertEqual(Server.url(base, "api/v1/search?q=\(query("cats & dogs?"))").absoluteString, "http://127.0.0.1:4810/api/v1/search?q=cats%20%26%20dogs%3F")
        XCTAssertEqual(Server.url(base, "api/v1/assets/abc/thumbnail").absoluteString, "http://127.0.0.1:4810/api/v1/assets/abc/thumbnail")
    }
}
