import CryptoKit
import Darwin
import Foundation

/// Адрес сервера kino platform, введённый человеком, → канонический вид.
///
/// «kino.example.com», «https://Kino.Example.com/», ссылка из адресной строки
/// витрины («…/#/title/42») — один и тот же сервер: https://kino.example.com.
/// От канонического вида считается профиль данных (см. ServerConfig.profile),
/// поэтому он обязан быть устойчивым: схема и хост в нижнем регистре, путь,
/// запрос, якорь и стандартный порт отброшены.
enum ServerAddress {

    static func normalize(_ raw: String) -> URL? {
        var s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        // Якорь витрины («/#/title/…») — часть адреса страницы, а не сервера.
        if let hash = s.firstIndex(of: "#") { s = String(s[..<hash]) }
        guard !s.isEmpty else { return nil }
        if !s.contains("://") { s = "https://" + s }
        guard let c = URLComponents(string: s),
              let scheme = c.scheme?.lowercased(), scheme == "http" || scheme == "https",
              var host = c.host?.lowercased(), !host.isEmpty
        else { return nil }
        // IPv6-литерал Foundation отдаёт то со скобками, то без — приводим к скобкам.
        if host.contains(":") && !host.hasPrefix("[") { host = "[\(host)]" }
        var out = "\(scheme)://\(host)"
        if let port = c.port, !(scheme == "https" && port == 443), !(scheme == "http" && port == 80) {
            out += ":\(port)"
        }
        return URL(string: out)
    }

    /// «kino.example.com» или «192.168.1.5:8080» — для подписи человеку.
    static func hostLabel(_ url: URL) -> String {
        guard let host = url.host else { return url.absoluteString }
        let shown = host.contains(":") ? "[\(host)]" : host
        return url.port.map { "\(shown):\($0)" } ?? shown
    }

    /// http без TLS: пароль и история идут по сети открытым текстом.
    static func isInsecure(_ url: URL) -> Bool { url.scheme?.lowercased() == "http" }

    /// Можно ли к этому адресу по http. Правило (то же на Android): https —
    /// всегда, http — только к серверу домашней сети. Сервер в интернете по
    /// http отдал бы пароль и историю каждому узлу по дороге.
    static func allowsCleartext(_ url: URL) -> Bool {
        guard isInsecure(url) else { return true }
        return url.host.map(isLocalHost) ?? false
    }

    /// Хост домашней сети: частные и служебные адреса, имя без точки и
    /// домашние суффиксы. Список общий с Android (ServerAddress.kt) — менять
    /// вместе.
    ///
    /// IPv4: 10/8, 172.16/12, 192.168/16, 127/8, 169.254/16, 100.64/10
    /// (CGNAT, Tailscale). IPv6: ::1, fc00::/7, fe80::/10. Имена: без точки,
    /// .local, .lan, .home, .home.arpa, .internal, .localhost.
    static func isLocalHost(_ raw: String) -> Bool {
        var host = raw.lowercased()
        if host.hasPrefix("["), host.hasSuffix("]") { host = String(host.dropFirst().dropLast()) }
        if host.hasSuffix(".") { host = String(host.dropLast()) }
        guard !host.isEmpty else { return false }
        if host.contains(":") { return isLocalIPv6(host) }
        if let ip = ipv4(host) { return isLocalIPv4(ip) }
        // Похоже на адрес, но не адрес (1.2.3, 300.1.1.1) — не имя и не local.
        if host.allSatisfy({ $0.isNumber || $0 == "." }) { return false }
        if !host.contains(".") { return true }
        return localSuffixes.contains { host.hasSuffix($0) }
    }

    private static let localSuffixes = [".local", ".lan", ".home", ".home.arpa", ".internal", ".localhost"]

    private static func ipv4(_ s: String) -> [Int]? {
        let parts = s.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 4 else { return nil }
        var out: [Int] = []
        for p in parts {
            guard !p.isEmpty, p.count <= 3, p.allSatisfy(\.isNumber), let n = Int(p), n <= 255 else { return nil }
            out.append(n)
        }
        return out
    }

    private static func isLocalIPv4(_ ip: [Int]) -> Bool {
        switch (ip[0], ip[1]) {
        case (10, _), (127, _): return true
        case (172, 16...31): return true
        case (192, 168): return true
        case (169, 254): return true
        case (100, 64...127): return true
        default: return false
        }
    }

    private static func isLocalIPv6(_ s: String) -> Bool {
        // Зона («fe80::1%en0») к адресу не относится.
        let addr = s.split(separator: "%", maxSplits: 1).first.map(String.init) ?? s
        var a = in6_addr()
        guard inet_pton(AF_INET6, addr, &a) == 1 else { return false }
        let b = withUnsafeBytes(of: a) { Array($0) }
        if b[0..<15].allSatisfy({ $0 == 0 }) && b[15] == 1 { return true } // ::1
        if b[0] & 0xFE == 0xFC { return true }                             // fc00::/7
        if b[0] == 0xFE && b[1] & 0xC0 == 0x80 { return true }             // fe80::/10
        return false
    }
}

/// Визитка сервера: GET /api/v1/server (без входа).
///
/// По ней приложение отличает сервер kino platform от любого сайта, ответившего
/// на введённый адрес, и решает, какую регистрацию показать.
struct ServerInfo: Codable, Equatable {
    var kind: String?
    var api: Int?
    var name: String?
    /// invite | open | closed; старый сервер без визитки — invite.
    var registration: String?
    var local: Bool?

    static let kino = "kino-platform"

    enum Registration { case invite, open, closed }

    var reg: Registration {
        switch registration {
        case "open": return .open
        case "closed": return .closed
        default: return .invite
        }
    }

    /// Сервер старше визитки (отвечает только /health): регистрация как раньше.
    static let legacy = ServerInfo(kind: kino, api: 1, name: nil, registration: "invite", local: nil)
}

/// Проверка введённого адреса до подключения.
enum ServerProbe {

    enum Failure: Error, Equatable {
        /// Не ответил вовсе: нет сети, не тот адрес, сервер лежит.
        case unreachable
        /// http к серверу в интернете: отказ без запроса в сеть.
        case cleartextPublic
        /// iOS не пустил по http (ATS). NSAllowsLocalNetworking пропускает
        /// частные IP (10/8, 172.16/12, 192.168/16, 127/8, 169.254/16, IPv6
        /// ULA и link-local), имя без точки и .local; режет 100.64/10
        /// (CGNAT, Tailscale) и имена .lan, .home, .home.arpa, .internal,
        /// *.localhost (проверено на симуляторе iOS 26.5). Хост — для подсказки.
        case insecureBlocked(host: String)
        /// Ответил, но это не kino platform.
        case notKino
        /// Ответил ошибкой сервера (5xx).
        case status(Int)

        var message: String {
            switch self {
            case .unreachable: return "Сервер не отвечает — проверьте адрес и сеть"
            case .cleartextPublic:
                return "По http — только сервер в домашней сети (192.168…, 10…, имя.local). Для сервера в интернете нужен https."
            case .insecureBlocked(let host):
                // Адрес 100.64/10 — уже IP, «укажите IP» тут не совет.
                if host.allSatisfy({ $0.isNumber || $0 == "." }) {
                    return "iOS пускает по http только адреса 192.168…, 10…, 172.16–31… — для этого адреса нужен https"
                }
                return "iOS пускает по http только IP-адреса и имена .local — укажите IP сервера или https"
            case .notKino: return "Это не сервер kino platform"
            case .status(let code): return "Сервер ответил ошибкой (\(code))"
            }
        }
    }

    /// Свой клиент: без cookie и сессии, короткие таймауты — человек ждёт ответа на кнопку.
    static let session: URLSession = {
        let cfg = URLSessionConfiguration.ephemeral
        cfg.httpShouldSetCookies = false
        cfg.httpCookieAcceptPolicy = .never
        cfg.requestCachePolicy = .reloadIgnoringLocalCacheData
        cfg.timeoutIntervalForRequest = 10
        cfg.timeoutIntervalForResource = 15
        return URLSession(configuration: cfg)
    }()

    static func fetch(_ base: URL, session: URLSession = session) async throws -> ServerInfo {
        // http в интернет — не спрашиваем вовсе: пароль туда открытым текстом не пойдёт.
        guard ServerAddress.allowsCleartext(base) else { throw Failure.cleartextPublic }
        let (data, code) = try await get(base.appendingPathComponent("api/v1/server"), session)
        switch code {
        case 200:
            guard let info = try? JSONDecoder().decode(ServerInfo.self, from: data), info.kind == ServerInfo.kino else {
                throw Failure.notKino
            }
            return info
        case 404:
            // Сервер старше визитки: свой — если отвечает /health как kino.
            let (health, hc) = try await get(base.appendingPathComponent("api/v1/health"), session)
            if hc == 200, let body = try? JSONDecoder().decode([String: String].self, from: health), body["status"] == "ok" {
                return .legacy
            }
            throw Failure.notKino
        case 500...:
            throw Failure.status(code)
        default:
            throw Failure.notKino
        }
    }

    private static func get(_ url: URL, _ session: URLSession) async throws -> (Data, Int) {
        var req = URLRequest(url: url)
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        do {
            let (data, rsp) = try await session.data(for: req)
            return (data, (rsp as? HTTPURLResponse)?.statusCode ?? 0)
        } catch let e as URLError where e.code == .appTransportSecurityRequiresSecureConnection {
            throw Failure.insecureBlocked(host: url.host ?? "")
        } catch {
            throw Failure.unreachable
        }
    }
}

/// Сервер, к которому подключено приложение, и пространство его данных.
///
/// Адрес не зашит в исходники: кто поднял свой сервер kino platform, вводит его
/// адрес и входит или регистрируется там. Сборка может вшить сервер по
/// умолчанию (KINO_URL в Config/Local.xcconfig → ключ KinoServerURL в
/// Info.plist); без него при первом запуске приложение просит указать адрес.
///
/// Идентификаторы тайтлов и серий у каждого сервера свои, поэтому всё, что на
/// них ссылается (сессия, опись скачанного, задания загрузки, кэш артов), у
/// каждого сервера лежит отдельно — в «профиле». Вшитый в сборку сервер держит
/// прежнее хранилище как было (profile == nil): установленные приложения после
/// обновления не разлогиниваются и не теряют скачанное. Любой другой сервер —
/// и любой вообще, если сборка сервера не вшила, — профиль «s-<8 hex>» от
/// хеша канонического адреса.
///
/// Настройки плеера и deviceId — свойства устройства, они общие.
enum ServerConfig {
    /// Ключ Info.plist с адресом сервера по умолчанию.
    static let infoKey = "KinoServerURL"

    /// Сервер по умолчанию, вшитый в сборку; nil — не вшит.
    static let defaultURL: URL? = builtIn(Bundle.main.infoDictionary)

    /// Адрес из Info.plist: пусто, нет ключа, неразвёрнутая переменная или не
    /// адрес — сервера по умолчанию нет.
    static func builtIn(_ info: [String: Any]?) -> URL? {
        guard let raw = info?[infoKey] as? String, !raw.contains("$(") else { return nil }
        return ServerAddress.normalize(raw)
    }

    private static let defaults = UserDefaults.standard
    private static let keyCurrent = "server_url"
    private static let keyRecent = "servers_recent"
    private static let maxRecent = 8

    /// Куда ходят запросы, пока сервер не выбран: зона .invalid не резолвится
    /// никогда, запрос честно падает «нет связи». Экраны в это время не
    /// работают — показан выбор сервера (needsServer).
    private static let nowhere = URL(string: "https://server.invalid")!

    private static let lock = NSLock()
    private static var _current: URL? = {
        if let s = UserDefaults.standard.string(forKey: keyCurrent), let u = ServerAddress.normalize(s) { return u }
        return defaultURL
    }()
    private static var _generation = 0

    /// Выбранный сервер (канонический адрес); nil — ещё не выбран, а сборка
    /// своего не вшила.
    static var current: URL? { lock.withLock { _current } }

    /// Сервера нет: вместо входа — «Укажите сервер kino platform».
    static var needsServer: Bool { current == nil }

    /// Номер подключения: растёт при каждой смене сервера. Фоновая работа,
    /// начатая при одном сервере, сверяет его перед тем, как писать результат.
    static var generation: Int { lock.withLock { _generation } }

    /// Профиль данных выбранного сервера; nil — вшитый в сборку (прежнее хранилище).
    static var profile: String? { current.flatMap { profile(for: $0) } }

    static func profile(for url: URL) -> String? { profile(for: url, builtIn: defaultURL) }

    /// Профиль сервера при данном вшитом сервере — отдельно ради тестов: в
    /// тестовой сборке вшитого сервера может не быть.
    static func profile(for url: URL, builtIn: URL?) -> String? {
        let canon = ServerAddress.normalize(url.absoluteString)?.absoluteString ?? url.absoluteString
        if let builtIn, canon == builtIn.absoluteString { return nil }
        let digest = SHA256.hash(data: Data(canon.utf8))
        return "s-" + digest.prefix(4).map { String(format: "%02x", $0) }.joined()
    }

    /// Куда ходят запросы. В DEBUG адрес выбранного сервера можно подменить
    /// окружением KINO_BASE_URL (прокси на Mac под Happ, мок) — профиль при
    /// этом остаётся профилем выбранного сервера.
    static var baseURL: URL {
        #if DEBUG
        if let s = ProcessInfo.processInfo.environment["KINO_BASE_URL"], let u = URL(string: s) { return u }
        #endif
        return current ?? nowhere
    }

    // MARK: - Известные серверы

    struct Known: Codable, Equatable, Identifiable {
        var url: String
        var name: String?
        var id: String { url }
        var address: URL? { URL(string: url) }
    }

    /// Недавние серверы, свежие первыми (выбранный — тоже здесь).
    static var recent: [Known] {
        guard let data = defaults.data(forKey: keyRecent),
              let list = try? JSONDecoder().decode([Known].self, from: data) else { return [] }
        return list
    }

    private static func saveRecent(_ list: [Known]) {
        if let data = try? JSONEncoder().encode(Array(list.prefix(maxRecent))) {
            defaults.set(data, forKey: keyRecent)
        }
    }

    /// Запомнить, как сервер назвал себя (визитка); порядок не трогаем.
    static func remember(_ url: URL, info: ServerInfo) {
        var list = recent
        let name = info.name?.trimmingCharacters(in: .whitespaces).nilIfEmpty
        if let i = list.firstIndex(where: { $0.url == url.absoluteString }) {
            guard list[i].name != name else { return }
            list[i].name = name
        } else {
            list.append(Known(url: url.absoluteString, name: name))
        }
        saveRecent(list)
    }

    /// Имя сервера или его хост — для подписи «Сервер: …».
    static func label(for url: URL) -> String {
        recent.first(where: { $0.url == url.absoluteString })?.name ?? ServerAddress.hostLabel(url)
    }

    static var label: String { current.map { label(for: $0) } ?? "не выбран" }

    /// Сменить сервер. Только сама запись — перестройку хранилищ и экранов
    /// делает ServerSwitch.apply.
    static func select(_ url: URL, info: ServerInfo?) {
        lock.withLock {
            _current = url
            _generation += 1
        }
        defaults.set(url.absoluteString, forKey: keyCurrent)
        var list = recent
        let old = list.first(where: { $0.url == url.absoluteString })
        list.removeAll { $0.url == url.absoluteString }
        let name = info?.name?.trimmingCharacters(in: .whitespaces).nilIfEmpty ?? old?.name
        list.insert(Known(url: url.absoluteString, name: name), at: 0)
        saveRecent(list)
    }

    /// Убрать из недавних (данные профиля остаются на устройстве).
    static func forget(_ url: URL) {
        saveRecent(recent.filter { $0.url != url.absoluteString })
    }
}

private extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}

extension Notification.Name {
    /// Сервер вот-вот сменится: закрыть плеер и экраны прежнего.
    static let serverWillChange = Notification.Name("kino.serverWillChange")
    /// Сервер сменён, хранилища перестроены: собрать корень заново.
    static let serverChanged = Notification.Name("kino.serverChanged")
}

/// Смена сервера на ходу — аналог перезапуска процесса на Android.
///
/// Порядок важен: сначала закрываются плеер и экраны (плеер на закрытии шлёт
/// последнюю позицию — она обязана уйти на прежний сервер, с его id серии),
/// потом меняется адрес и перестраиваются хранилища, и только затем корень
/// собирается заново с новым AppState.
@MainActor
enum ServerSwitch {

    static func apply(_ url: URL, info: ServerInfo?) async {
        guard url != ServerConfig.current else {
            if let info { ServerConfig.remember(url, info: info) }
            return
        }
        LogUI.event("server switch -> \(url.absoluteString)")
        NotificationCenter.default.post(name: .serverWillChange, object: nil)
        // Плеер и экраны гаснут; их последние запросы (позиция, метки) успевают
        // собраться на прежний адрес. Ответы на них ApiClient к новому серверу
        // не применяет (см. Api.isCurrentServer).
        try? await Task.sleep(nanoseconds: 600_000_000)
        ServerConfig.select(url, info: info)
        Api.shared.reloadSession()
        LocalLibrary.shared.reload()
        ArtStore.shared.reload()
        DownloadManager.shared.serverChanged()
        NotificationCenter.default.post(name: .serverChanged, object: nil)
    }
}
