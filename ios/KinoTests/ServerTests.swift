import XCTest
@testable import Kino

/// Адрес сервера: канонический вид и профиль данных от него.
final class ServerAddressTests: XCTestCase {

    func testNormalize() {
        let cases: [(String, String)] = [
            ("kino.example.com", "https://kino.example.com"),
            ("  Kino.Example.COM/  ", "https://kino.example.com"),
            ("https://kino.example.com/", "https://kino.example.com"),
            ("HTTPS://kino.example.com", "https://kino.example.com"),
            // Ссылка из адресной строки витрины.
            ("https://kino.example.com/#/title/42/s1e2", "https://kino.example.com"),
            ("kino.example.com/#/title/42", "https://kino.example.com"),
            // Путь и запрос — не часть сервера.
            ("https://kino.example.com/some/path?x=1", "https://kino.example.com"),
            // Стандартный порт отбрасывается, нестандартный остаётся.
            ("https://kino.example.com:443", "https://kino.example.com"),
            ("http://kino.home.lan:80", "http://kino.home.lan"),
            ("kino.example.com:8443", "https://kino.example.com:8443"),
            ("http://192.168.1.5:8080", "http://192.168.1.5:8080"),
            ("http://[::1]:8080", "http://[::1]:8080"),
        ]
        for (raw, want) in cases {
            XCTAssertEqual(ServerAddress.normalize(raw)?.absoluteString, want, raw)
        }
    }

    func testNormalizeRejectsGarbage() {
        for raw in ["", "   ", "#/title/1", "ftp://kino.example.com", "https://", "kino example com"] {
            XCTAssertNil(ServerAddress.normalize(raw), raw)
        }
    }

    func testInsecureAndLabel() {
        XCTAssertTrue(ServerAddress.isInsecure(URL(string: "http://kino.home.lan")!))
        XCTAssertFalse(ServerAddress.isInsecure(URL(string: "https://kino.example.com")!))
        XCTAssertEqual(ServerAddress.hostLabel(URL(string: "http://192.168.1.5:8080")!), "192.168.1.5:8080")
        XCTAssertEqual(ServerAddress.hostLabel(URL(string: "https://kino.example.com")!), "kino.example.com")
    }

    /// Вшитый в сборку сервер — прежнее хранилище (nil); остальные — устойчивый
    /// профиль от канонического адреса. Без вшитого сервера профиль свой у всех.
    func testProfile() {
        let builtIn = URL(string: "https://kino.example.org")!
        XCTAssertNil(ServerConfig.profile(for: builtIn, builtIn: builtIn))
        XCTAssertNil(ServerConfig.profile(for: URL(string: "https://KINO.example.org/")!, builtIn: builtIn))
        let a = ServerConfig.profile(for: URL(string: "https://kino.example.com")!, builtIn: builtIn)
        XCTAssertNotNil(a)
        XCTAssertTrue(a!.hasPrefix("s-"))
        XCTAssertEqual(a!.count, 10)
        // Тот же сервер, записанный иначе, — тот же профиль.
        XCTAssertEqual(ServerConfig.profile(for: URL(string: "https://KINO.example.com/")!, builtIn: builtIn), a)
        // http и https, другой порт — разные серверы (разные сессии).
        XCTAssertNotEqual(ServerConfig.profile(for: URL(string: "http://kino.example.com")!, builtIn: builtIn), a)
        XCTAssertNotEqual(ServerConfig.profile(for: URL(string: "https://kino.example.com:8443")!, builtIn: builtIn), a)
        // Профиль от адреса не зависит от того, какой сервер вшит.
        XCTAssertEqual(ServerConfig.profile(for: URL(string: "https://kino.example.com")!, builtIn: nil), a)
        // Сборка без вшитого сервера: прежнего хранилища нет ни у кого.
        XCTAssertNotNil(ServerConfig.profile(for: builtIn, builtIn: nil))
    }

    /// Адрес по умолчанию — из Info.plist сборки (KINO_URL); пусто, нет ключа,
    /// неразвёрнутая переменная или мусор — сервера по умолчанию нет.
    func testBuiltInFromInfoPlist() {
        let key = ServerConfig.infoKey
        XCTAssertEqual(ServerConfig.builtIn([key: "https://kino.example.org/"])?.absoluteString, "https://kino.example.org")
        XCTAssertEqual(ServerConfig.builtIn([key: "kino.example.org"])?.absoluteString, "https://kino.example.org")
        XCTAssertNil(ServerConfig.builtIn([key: ""]))
        XCTAssertNil(ServerConfig.builtIn([key: "$(KINO_URL)"]))
        XCTAssertNil(ServerConfig.builtIn([key: "not a url"]))
        XCTAssertNil(ServerConfig.builtIn([:]))
        XCTAssertNil(ServerConfig.builtIn(nil))
    }
}

/// Данные разных серверов не смешиваются.
final class ServerStorageTests: XCTestCase {

    func testStorageLocationsPerProfile() {
        // Вшитый сервер (profile == nil) — ровно прежние места: обновление не
        // разлогинивает и не теряет скачанное.
        XCTAssertEqual(TokenStore.account(for: nil), "session")
        XCTAssertEqual(LocalLibrary.fileURL(profile: nil).lastPathComponent, "library.json")
        XCTAssertEqual(LocalLibrary.fileURL(profile: nil).deletingLastPathComponent().lastPathComponent, "Application Support")
        XCTAssertEqual(ArtStore.dir(profile: nil).lastPathComponent, "art")

        XCTAssertEqual(TokenStore.account(for: "s-1a2b3c4d"), "session-s-1a2b3c4d")
        XCTAssertEqual(LocalLibrary.fileURL(profile: "s-1a2b3c4d").deletingLastPathComponent().lastPathComponent, "s-1a2b3c4d")
        XCTAssertEqual(ArtStore.dir(profile: "s-1a2b3c4d").lastPathComponent, "art-s-1a2b3c4d")
        XCTAssertNotEqual(LocalLibrary.fileURL(profile: "s-1a2b3c4d"), LocalLibrary.fileURL(profile: "s-99999999"))
    }

    /// Сессия одного сервера не видна другому. Профили — тестовые: запись
    /// сервера по умолчанию («session») на симуляторе может быть настоящей.
    func testKeychainSessionsSeparated() {
        let a = "s-test0001", b = "s-test0002"
        defer { TokenStore.clear(profile: a); TokenStore.clear(profile: b) }
        TokenStore.clear(profile: a)
        TokenStore.clear(profile: b)
        TokenStore.save(TokenPair(token: "tok-a", refreshToken: "ref-a"), profile: a)
        XCTAssertEqual(TokenStore.load(profile: a)?.token, "tok-a")
        XCTAssertNil(TokenStore.load(profile: b))
        XCTAssertNotEqual(TokenStore.load(profile: nil)?.token, "tok-a")
        TokenStore.save(TokenPair(token: "tok-b", refreshToken: "ref-b"), profile: b)
        XCTAssertEqual(TokenStore.refreshToken(profile: a), "ref-a")
        XCTAssertEqual(TokenStore.refreshToken(profile: b), "ref-b")
        TokenStore.clear(profile: a)
        XCTAssertNil(TokenStore.load(profile: a))
        XCTAssertEqual(TokenStore.load(profile: b)?.token, "tok-b")
    }

    @MainActor
    func testLibrariesSeparated() throws {
        let dir = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("libtest-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let media = dir.appendingPathComponent("1.movpkg")
        try Data("x".utf8).write(to: media)

        let libA = LocalLibrary(file: dir.appendingPathComponent("a/library.json"))
        libA.put(LocalLibrary.Entry(
            id: "5:1:1", titleId: 5, title: "Тьма", season: 1, episode: 1,
            uri: LocalLibrary.storedPath(media), size: 1, addedAt: 0,
            positionMs: 0, durationMs: 0, watched: false, dirty: false, movie: false
        ))
        // Тот же entryId у другого сервера — другая опись, другая серия.
        let libB = LocalLibrary(file: dir.appendingPathComponent("b/library.json"))
        XCTAssertNil(libB.entry("5:1:1"))
        // Опись переживает перечитывание с диска.
        XCTAssertEqual(LocalLibrary(file: dir.appendingPathComponent("a/library.json")).entry("5:1:1")?.title, "Тьма")
    }

    /// Ключ задания загрузки: у сервера по умолчанию — прежний (entryId),
    /// у другого — с профилем, и entryId из него достаётся обратно.
    func testDownloadKeys() {
        XCTAssertEqual(DownloadManager.key(entryId: "5:1:2", profile: nil), "5:1:2")
        let k = DownloadManager.key(entryId: "5:1:2", profile: "s-1a2b3c4d")
        XCTAssertEqual(k, "s-1a2b3c4d|5:1:2")
        XCTAssertEqual(DownloadManager.entryId(fromKey: k), "5:1:2")
        XCTAssertEqual(DownloadManager.entryId(fromKey: "5:1:2"), "5:1:2")
    }
}

/// Визитка сервера: свой сервер, старый сервер без визитки, чужой сайт.
final class ServerProbeTests: XCTestCase {

    /// Ответы по пути запроса; нет пути — сеть недоступна.
    final class Stub: URLProtocol {
        nonisolated(unsafe) static var routes: [String: (Int, String)] = [:]
        /// Сколько запросов дошло до «сети».
        nonisolated(unsafe) static var requests = 0

        override class func canInit(with request: URLRequest) -> Bool { true }
        override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
        override func startLoading() {
            Self.requests += 1
            guard let url = request.url, let (code, body) = Self.routes[url.path] else {
                client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
                return
            }
            let rsp = HTTPURLResponse(url: url, statusCode: code, httpVersion: "HTTP/1.1", headerFields: nil)!
            client?.urlProtocol(self, didReceive: rsp, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(body.utf8))
            client?.urlProtocolDidFinishLoading(self)
        }
        override func stopLoading() {}
    }

    private let base = URL(string: "https://kino.example.com")!

    private func session() -> URLSession {
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [Stub.self]
        return URLSession(configuration: cfg)
    }

    func testKinoServer() async throws {
        Stub.routes = ["/api/v1/server": (200, #"{"kind":"kino-platform","api":1,"name":"Кино друзей","registration":"open","extra":1}"#)]
        let info = try await ServerProbe.fetch(base, session: session())
        XCTAssertEqual(info.name, "Кино друзей")
        XCTAssertEqual(info.reg, .open)
    }

    func testLegacyServerFallsBackToHealth() async throws {
        Stub.routes = [
            "/api/v1/server": (404, #"{"error":"not_found"}"#),
            "/api/v1/health": (200, #"{"status":"ok"}"#),
        ]
        let info = try await ServerProbe.fetch(base, session: session())
        XCTAssertEqual(info, .legacy)
        XCTAssertEqual(info.reg, .invite)
        XCTAssertNil(info.name)
    }

    func testForeignSite() async {
        // Сайт, который на любой путь отвечает страницей.
        Stub.routes = ["/api/v1/server": (200, "<html>hello</html>")]
        await assertFails(.notKino)
        // Чужой JSON с 200.
        Stub.routes = ["/api/v1/server": (200, #"{"kind":"jellyfin"}"#)]
        await assertFails(.notKino)
        // 404 и на визитку, и на /health.
        Stub.routes = ["/api/v1/server": (404, ""), "/api/v1/health": (404, "")]
        await assertFails(.notKino)
    }

    func testUnreachableAndServerError() async {
        Stub.routes = [:]
        await assertFails(.unreachable)
        Stub.routes = ["/api/v1/server": (502, "Bad Gateway")]
        await assertFails(.status(502))
    }

    private func assertFails(_ want: ServerProbe.Failure, line: UInt = #line) async {
        do {
            _ = try await ServerProbe.fetch(base, session: session())
            XCTFail("ждали \(want)", line: line)
        } catch let f as ServerProbe.Failure {
            XCTAssertEqual(f, want, line: line)
        } catch {
            XCTFail("\(error)", line: line)
        }
    }

    func testRegistrationModes() throws {
        func reg(_ s: String?) -> ServerInfo.Registration {
            ServerInfo(kind: ServerInfo.kino, api: 1, name: nil, registration: s, local: nil).reg
        }
        XCTAssertEqual(reg("invite"), .invite)
        XCTAssertEqual(reg("open"), .open)
        XCTAssertEqual(reg("closed"), .closed)
        // Незнакомое и пустое — как раньше, по приглашению.
        XCTAssertEqual(reg(nil), .invite)
        XCTAssertEqual(reg("whatever"), .invite)
    }
}

/// http — только к серверу домашней сети; список тот же, что на Android.
final class LocalHostTests: XCTestCase {

    func testLocalRanges() {
        let local = [
            "10.0.0.1", "10.255.255.254",              // 10/8
            "172.16.0.1", "172.31.255.254",            // 172.16/12
            "192.168.0.1", "192.168.255.254",          // 192.168/16
            "127.0.0.1", "127.8.8.8",                  // 127/8
            "169.254.0.1", "169.254.255.254",          // 169.254/16
            "100.64.0.1", "100.127.255.254",           // 100.64/10 (CGNAT, Tailscale)
            "::1", "[::1]",                            // петля
            "fc00::1", "fd12:3456::1",                 // fc00::/7
            "fe80::1", "febf::1", "fe80::1%en0",       // fe80::/10
            "kinoserver", "localhost",                 // имя без точки
            "kino.local", "KINO.LOCAL", "kino.lan", "nas.home", "kino.home.arpa",
            "kino.internal", "kino.localhost", "kino.local.",
        ]
        for h in local { XCTAssertTrue(ServerAddress.isLocalHost(h), h) }
    }

    func testNotLocal() {
        let remote = [
            "172.15.255.254", "172.32.0.1",            // границы 172.16/12
            "100.63.255.254", "100.128.0.1",           // границы 100.64/10
            "192.169.0.1", "169.255.0.1", "11.0.0.1",
            "8.8.8.8", "1.1.1.1",
            "2001:db8::1", "fbff::1", "fec0::1",       // мимо fc00::/7 и fe80::/10
            "kino.example.org", "kino.example.com", "example.local.org", "lan.example.org",
            "1.2.3", "300.1.1.1", "",
        ]
        for h in remote { XCTAssertFalse(ServerAddress.isLocalHost(h), h) }
    }

    func testAllowsCleartext() {
        // https — всегда.
        XCTAssertTrue(ServerAddress.allowsCleartext(URL(string: "https://kino.example.org")!))
        XCTAssertTrue(ServerAddress.allowsCleartext(URL(string: "https://192.168.1.5")!))
        // http — только домашняя сеть.
        XCTAssertTrue(ServerAddress.allowsCleartext(URL(string: "http://192.168.1.5:8080")!))
        XCTAssertTrue(ServerAddress.allowsCleartext(URL(string: "http://kino.local")!))
        XCTAssertTrue(ServerAddress.allowsCleartext(ServerAddress.normalize("http://[::1]:8080")!))
        XCTAssertFalse(ServerAddress.allowsCleartext(URL(string: "http://kino.example.org")!))
        XCTAssertFalse(ServerAddress.allowsCleartext(URL(string: "http://8.8.8.8")!))
        XCTAssertFalse(ServerAddress.allowsCleartext(URL(string: "http://172.32.0.1")!))
    }

    /// iOS режет по http часть домашних хостов (100.64/10, .lan, .home.arpa…):
    /// подсказка различает IP и имя.
    func testInsecureBlockedMessages() {
        XCTAssertTrue(ServerProbe.Failure.insecureBlocked(host: "100.100.1.1").message.contains("нужен https"))
        XCTAssertTrue(ServerProbe.Failure.insecureBlocked(host: "kino.lan").message.contains("укажите IP"))
    }

    /// http к серверу в интернете отвергается до сети: ни одного запроса.
    func testCleartextPublicRejectedWithoutRequest() async {
        ServerProbeTests.Stub.routes = [:]
        ServerProbeTests.Stub.requests = 0
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [ServerProbeTests.Stub.self]
        do {
            _ = try await ServerProbe.fetch(URL(string: "http://kino.example.org")!, session: URLSession(configuration: cfg))
            XCTFail("ждали отказа")
        } catch let f as ServerProbe.Failure {
            XCTAssertEqual(f, .cleartextPublic)
            XCTAssertTrue(f.message.contains("https"))
        } catch {
            XCTFail("\(error)")
        }
        XCTAssertEqual(ServerProbeTests.Stub.requests, 0)

        // Домашний http — спрашиваем как обычно.
        ServerProbeTests.Stub.routes = ["/api/v1/server": (200, #"{"kind":"kino-platform","registration":"invite"}"#)]
        let info = try? await ServerProbe.fetch(URL(string: "http://192.168.1.5:8080")!, session: URLSession(configuration: cfg))
        XCTAssertEqual(info?.reg, .invite)
        XCTAssertEqual(ServerProbeTests.Stub.requests, 1)
    }
}
