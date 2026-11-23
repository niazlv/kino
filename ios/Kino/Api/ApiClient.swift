import Foundation

/// Ошибка API — контракт `{"error": "<код>"}`.
struct ApiError: Error, Equatable {
    let status: Int
    let code: String
    let message: String

    /// 401/403 — сессия невалидна.
    var unauthorized: Bool { status == 401 || status == 403 }
    /// Сессия больше не годится: 401/403 или 410 «expired» — так сервер
    /// отвечает на истёкший токен (UserSessionTTL 30 дней, без продления на
    /// ходу) и на истёкший refresh. Повод для refresh, а не провал повтора — на вход.
    var sessionDead: Bool { unauthorized || (status == 410 && code == "expired") }
    /// Сеть недоступна/оборвалась.
    var offline: Bool { status == 0 }

    static let offline = ApiError(status: 0, code: "offline", message: "Нет связи с сервером")

    /// Подпись для человека (сырой код сервера на экран не выводим).
    var userMessage: String {
        if offline { return "Нет связи с сервером" }
        if sessionDead { return "Сессия истекла — войдите заново" }
        switch status {
        case 404: return "Не найдено"
        case 502, 503: return "Площадка не отвечает — попробуйте позже"
        case 500...: return "Сервер не отвечает (\(status))"
        default: return "Ошибка \(status)"
        }
    }
}

/// HTTP-клиент платформы — зеркало data/Api.kt.
///
/// Авторизация — ручной заголовок `Cookie: kino_session=…` (как на Android);
/// авто-куки URLSession выключены, чтобы не плодить вторую сессию из Set-Cookie.
/// По 401/403 один раз освежает пару через /auth/refresh (single-flight) и
/// повторяет запрос.
final class Api: @unchecked Sendable {
    static let shared = Api()

    /// Адрес выбранного сервера (см. ServerConfig): не зашит в сборку, человек
    /// вводит свой. В DEBUG можно перекрыть окружением KINO_BASE_URL (прокси, мок).
    static var baseURL: URL { ServerConfig.baseURL }

    private let session: URLSession   // обычные запросы: connect 10 с, read 40 с
    private let bare: URLSession      // login/refresh: read 20 с, без refresh-петли
    /// Постеры и кадры — свой пул соединений: по HTTP/1.1 их десятки, и в
    /// общем пуле запрос списка ждал своей очереди за картинками.
    private let artSession: URLSession
    private let lock = NSLock()
    private var _token: String?
    private var _refreshTask: Task<Bool, Never>?

    var token: String? {
        get { lock.lock(); defer { lock.unlock() }; return _token }
        set { lock.lock(); _token = newValue; lock.unlock() }
    }

    private init() {
        let cfg = URLSessionConfiguration.ephemeral
        cfg.httpShouldSetCookies = false
        cfg.httpCookieAcceptPolicy = .never
        cfg.requestCachePolicy = .reloadIgnoringLocalCacheData
        cfg.timeoutIntervalForRequest = 40
        cfg.timeoutIntervalForResource = 120
        session = URLSession(configuration: cfg)

        let bareCfg = cfg.copy() as! URLSessionConfiguration
        bareCfg.timeoutIntervalForRequest = 20
        bare = URLSession(configuration: bareCfg)

        let artCfg = cfg.copy() as! URLSessionConfiguration
        artCfg.httpMaximumConnectionsPerHost = 4
        artSession = URLSession(configuration: artCfg)

        if let pair = TokenStore.load() { _token = pair.token }
    }

    /// Сервер сменён: сессия — из его профиля, начатое продление прежнего забыто
    /// (его итог допишется в профиль прежнего сервера, см. refreshOnce).
    func reloadSession() {
        let pair = TokenStore.load()
        lock.withLock {
            _token = pair?.token
            _refreshTask = nil
        }
    }

    /// Запрос ушёл на выбранный сейчас сервер? Ответ на запрос, собранный до
    /// смены сервера, не повод продлевать или гасить сессию нового.
    static func isCurrentServer(_ req: URLRequest) -> Bool {
        guard let url = req.url else { return false }
        let base = baseURL
        return url.scheme == base.scheme && url.host == base.host && url.port == base.port
    }

    // MARK: - Низкий уровень

    func build(
        _ method: String, _ path: String, query: [URLQueryItem],
        body: Data?, timeout: TimeInterval?, bareClient: Bool
    ) -> URLRequest {
        var comps = URLComponents(url: Self.baseURL.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty { comps.queryItems = query }
        var req = URLRequest(url: comps.url!)
        req.httpMethod = method
        if let t = token, !t.isEmpty {
            req.setValue("kino_session=\(t)", forHTTPHeaderField: "Cookie")
        }
        if let body {
            req.httpBody = body
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if let timeout { req.timeoutInterval = timeout }
        return req
    }

    /// Ответ или ApiError. `retryAuth` — можно ли освежить сессию по 401.
    func send(_ req: URLRequest, bareClient: Bool, retryAuth: Bool, art: Bool = false) async throws -> (Data, HTTPURLResponse) {
        let client = art ? artSession : (bareClient ? bare : session)
        let data: Data
        let rsp: URLResponse
        do {
            (data, rsp) = try await client.data(for: req)
        } catch let e as URLError {
            throw ApiError(status: 0, code: "offline", message: e.localizedDescription)
        }
        let http = rsp as! HTTPURLResponse
        if (200..<300).contains(http.statusCode) {
            return (data, http)
        }
        let code = (try? JSONDecoder().decode(ErrorBody.self, from: data))?.error ?? ""
        let failure = ApiError(status: http.statusCode, code: code, message: code)
        if failure.sessionDead, Self.isCurrentServer(req) {
            if retryAuth, !bareClient {
                // Токен, с которым ушёл запрос: его могли уже сменить соседи.
                let sent = Self.sessionToken(in: req)
                if let now = token, !now.isEmpty, now != sent {
                    return try await send(withToken(req, now), bareClient: bareClient, retryAuth: false, art: art)
                }
                if await refreshOnce(), let now = token {
                    // Cookie вшит в запрос при сборке — повтор со свежим токеном.
                    return try await send(withToken(req, now), bareClient: bareClient, retryAuth: false, art: art)
                }
                // Парольная сессия (без refresh) истекла — продлевать нечем:
                // на вход, а не ошибки на каждом экране.
                // 410 «expired» бывает и не про сессию (истёкший код устройства) —
                // выкидываем на вход, только если /me подтверждает.
                if http.statusCode != 403, (TokenStore.refreshToken() ?? "").isEmpty, await sessionGone() {
                    await MainActor.run { NotificationCenter.default.post(name: .sessionExpired, object: nil) }
                }
            }
        }
        throw failure
    }

    private struct ErrorBody: Codable { let error: String }

    /// Текущая сессия мертва? Проверка /me без refresh-петли.
    private func sessionGone() async -> Bool {
        let req = build("GET", "api/v1/me", query: [], body: nil, timeout: 15, bareClient: false)
        do {
            _ = try await send(req, bareClient: false, retryAuth: false)
            return false
        } catch let e as ApiError {
            return e.sessionDead
        } catch {
            return false
        }
    }

    private static func sessionToken(in req: URLRequest) -> String? {
        guard let c = req.value(forHTTPHeaderField: "Cookie"), c.hasPrefix("kino_session=") else { return nil }
        return String(c.dropFirst("kino_session=".count))
    }

    private func withToken(_ req: URLRequest, _ token: String) -> URLRequest {
        var r = req
        r.setValue("kino_session=\(token)", forHTTPHeaderField: "Cookie")
        return r
    }

    /// Однократный refresh на всех ожидающих: первый 401 запускает задачу,
    /// остальные ждут её же. Возвращает true, если пара сменилась.
    private func refreshOnce() async -> Bool {
        // Профиль — на старте: если сервер сменят, пока продление идёт, новая
        // пара прежнего сервера ляжет в его профиль, а не в профиль нового.
        let profile = ServerConfig.profile
        let generation = ServerConfig.generation
        let task: Task<Bool, Never>? = lock.withLock {
            if let running = _refreshTask { return running }
            guard let refreshToken = TokenStore.refreshToken(profile: profile), !refreshToken.isEmpty else { return nil }
            let t = Task<Bool, Never> { [weak self] in
                guard let self else { return false }
                do {
                    let pair: TokenPair = try await self.rawPost("api/v1/auth/refresh", body: RefreshBody(refreshToken: refreshToken))
                    self.savePair(pair, profile: profile)
                    return ServerConfig.generation == generation
                } catch {
                    if let e = error as? ApiError, e.sessionDead, ServerConfig.generation == generation {
                        await MainActor.run { NotificationCenter.default.post(name: .sessionExpired, object: nil) }
                    }
                    return false
                }
            }
            _refreshTask = t
            return t
        }
        guard let task else { return false }
        let ok = await task.value
        lock.withLock {
            if _refreshTask == task { _refreshTask = nil }
        }
        return ok
    }

    private func savePair(_ pair: TokenPair, profile: String? = ServerConfig.profile) {
        TokenStore.save(pair, profile: profile)
        if profile == ServerConfig.profile { token = pair.token }
    }

    // MARK: - Типовые вызовы

    func jsonBody<B: Encodable>(_ body: B?) throws -> Data? {
        guard let body else { return nil }
        return try JSONEncoder().encode(body)
    }

    func get<T: Decodable>(_ path: String, query: [URLQueryItem] = [], timeout: TimeInterval? = nil) async throws -> T {
        let req = build("GET", path, query: query, body: nil, timeout: timeout, bareClient: false)
        let (data, _) = try await send(req, bareClient: false, retryAuth: true)
        return try JSONDecoder().decode(T.self, from: data)
    }

    func post<B: Encodable, T: Decodable>(_ path: String, body: B, timeout: TimeInterval? = nil) async throws -> T {
        let req = build("POST", path, query: [], body: try jsonBody(body), timeout: timeout, bareClient: false)
        let (data, _) = try await send(req, bareClient: false, retryAuth: true)
        return try JSONDecoder().decode(T.self, from: data)
    }

    func postUnit<B: Encodable>(_ path: String, body: B) async throws {
        let req = build("POST", path, query: [], body: try jsonBody(body), timeout: nil, bareClient: false)
        _ = try await send(req, bareClient: false, retryAuth: true)
    }

    func postEmpty<T: Decodable>(_ path: String, timeout: TimeInterval? = nil) async throws -> T {
        let req = build("POST", path, query: [], body: nil, timeout: timeout, bareClient: false)
        let (data, _) = try await send(req, bareClient: false, retryAuth: true)
        return try JSONDecoder().decode(T.self, from: data)
    }

    /// POST без тела и без разбора ответа (204: approve/deny/logout).
    func postNoBody(_ path: String) async throws {
        let req = build("POST", path, query: [], body: nil, timeout: nil, bareClient: false)
        _ = try await send(req, bareClient: false, retryAuth: true)
    }

    func put<B: Encodable>(_ path: String, body: B) async throws {
        let req = build("PUT", path, query: [], body: try jsonBody(body), timeout: nil, bareClient: false)
        _ = try await send(req, bareClient: false, retryAuth: true)
    }

    func putUnit<B: Encodable>(_ path: String, body: B) async throws -> Data {
        let req = build("PUT", path, query: [], body: try jsonBody(body), timeout: nil, bareClient: false)
        let (data, _) = try await send(req, bareClient: false, retryAuth: true)
        return data
    }

    func delete(_ path: String, query: [URLQueryItem] = []) async throws {
        let req = build("DELETE", path, query: query, body: nil, timeout: nil, bareClient: false)
        _ = try await send(req, bareClient: false, retryAuth: true)
    }

    func deleteRsp<T: Decodable>(_ path: String, query: [URLQueryItem] = []) async throws -> T {
        let req = build("DELETE", path, query: query, body: nil, timeout: nil, bareClient: false)
        let (data, _) = try await send(req, bareClient: false, retryAuth: true)
        return try JSONDecoder().decode(T.self, from: data)
    }

    /// Вызов без авторизации и без refresh-петли (login/register/device-poll).
    func barePost<B: Encodable, T: Decodable>(_ path: String, body: B) async throws -> T {
        let req = build("POST", path, query: [], body: try jsonBody(body), timeout: nil, bareClient: true)
        let (data, _) = try await send(req, bareClient: true, retryAuth: false)
        return try JSONDecoder().decode(T.self, from: data)
    }

    /// POST, ответ которого — Set-Cookie с токеном сессии.
    func loginPost<B: Encodable>(_ path: String, body: B) async throws -> (cookie: String?, body: Data) {
        let req = build("POST", path, query: [], body: try jsonBody(body), timeout: nil, bareClient: true)
        let (data, http) = try await send(req, bareClient: true, retryAuth: false)
        // Несколько Set-Cookie Foundation склеивает через запятую — разбираем
        // штатным парсером, а не по префиксу строки.
        return (Self.sessionCookie(http).map { "kino_session=\($0)" }, data)
    }

    /// Запрос с текущей сессией, ответ которого может выдать новую сессию в
    /// Set-Cookie (смена пароля). Без refresh-петли: старая сессия гаснет.
    func cookieCall<B: Encodable>(_ method: String, _ path: String, body: B) async throws -> (cookie: String?, body: Data) {
        let req = build(method, path, query: [], body: try jsonBody(body), timeout: nil, bareClient: false)
        let (data, http) = try await send(req, bareClient: false, retryAuth: false)
        return (Self.sessionCookie(http), data)
    }

    /// kino_session из Set-Cookie; пустая (сброс) — nil.
    static func sessionCookie(_ http: HTTPURLResponse) -> String? {
        guard let headers = http.allHeaderFields as? [String: String], let url = http.url else { return nil }
        return HTTPCookie.cookies(withResponseHeaderFields: headers, for: url)
            .first(where: { $0.name == "kino_session" && !$0.value.isEmpty })
            .map(\.value)
    }

    /// «Голый» POST без разбора тела (logout старой сессии).
    func rawPost<B: Encodable, T: Decodable>(_ path: String, body: B) async throws -> T {
        let req = build("POST", path, query: [], body: try jsonBody(body), timeout: nil, bareClient: true)
        let (data, _) = try await send(req, bareClient: true, retryAuth: false)
        return try JSONDecoder().decode(T.self, from: data)
    }

    // MARK: - Арты

    /// Относительные адреса потоков/субтитров → абсолютные.
    static func absolute(_ url: String) -> URL? {
        if url.hasPrefix("http://") || url.hasPrefix("https://") { return URL(string: url) }
        return URL(string: url, relativeTo: baseURL)?.absoluteURL
    }

    /// JPEG постера/кадра с сессией (для загрузчика картинок).
    func artData(_ path: String) async throws -> Data {
        let req = build("GET", path, query: [], body: nil, timeout: nil, bareClient: false)
        let (data, _) = try await send(req, bareClient: false, retryAuth: true, art: true)
        return data
    }

    // MARK: - Сессия

    func saveSession(_ pair: TokenPair) { savePair(pair) }

    func clearSession() {
        TokenStore.clear()
        token = nil
    }
}

extension Notification.Name {
    static let sessionExpired = Notification.Name("kino.sessionExpired")
}
