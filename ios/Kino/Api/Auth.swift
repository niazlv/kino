import Foundation

/// Вход/выход/подключение устройств — зеркало data/Auth.kt.
///
/// Парольная сессия живёт 30 дней без refresh-токена, поэтому сразу после
/// входа приложение само одобряет себе device-flow и дальше живёт на паре
/// токен/refresh (ротация при каждом обновлении, refresh живёт 180 дней).
enum Auth {

    /// Вид устройства для сервера: витрина и админка знают «ios» («iPhone / iPad»).
    static let deviceKind = "ios"

    /// Вход по паролю: cookie из Set-Cookie → device-flow → пара токенов →
    /// гашение парольной сессии.
    static func login(login: String, password: String) async throws {
        let api = Api.shared
        let rsp = try await api.loginPost("api/v1/auth/login", body: LoginBody(login: login, password: password, device: deviceKind))
        guard let cookie = rsp.cookie, cookie.hasPrefix("kino_session=") else {
            throw ApiError(status: 401, code: "no_session", message: "Сервер не выдал сессию")
        }
        let oldToken = String(cookie.dropFirst("kino_session=".count))
        // Парольную сессию — сразу на диск: не выйдет обмен на пару, вход
        // всё равно переживёт перезапуск (месяц, как на Android).
        api.saveSession(TokenPair(token: oldToken, refreshToken: ""))

        if let pair = await upgradeToRefreshable() {
            api.saveSession(pair)
            // Парольная сессия больше не нужна — иначе месяц висела бы в
            // «Где вы вошли» вторым входом.
            var req = api.build("POST", "api/v1/auth/logout", query: [], body: nil, timeout: nil, bareClient: true)
            req.setValue("kino_session=\(oldToken)", forHTTPHeaderField: "Cookie")
            _ = try? await api.send(req, bareClient: true, retryAuth: false)
        }
    }

    /// Смена пароля. Сервер гасит все сессии (и пару с refresh) и выдаёт новую
    /// парольную в Set-Cookie — её сохраняем и снова меняем на пару, иначе
    /// следующий же запрос выкинул бы на вход.
    static func changePassword(old: String, new: String) async throws {
        let api = Api.shared
        let rsp = try await api.cookieCall("PUT", "api/v1/me/password", body: PasswordBody(oldPassword: old, newPassword: new))
        guard let token = rsp.cookie else {
            // {"reloginRequired": true}: пароль сменён, сессии нет — на вход.
            await MainActor.run { NotificationCenter.default.post(name: .sessionExpired, object: nil) }
            return
        }
        api.saveSession(TokenPair(token: token, refreshToken: ""))
        if let pair = await upgradeToRefreshable() {
            api.saveSession(pair)
            var req = api.build("POST", "api/v1/auth/logout", query: [], body: nil, timeout: nil, bareClient: true)
            req.setValue("kino_session=\(token)", forHTTPHeaderField: "Cookie")
            _ = try? await api.send(req, bareClient: true, retryAuth: false)
        }
    }

    /// Регистрация по приглашению: сервер сразу отвечает Me.
    static func register(invite: String, login: String, password: String, displayName: String) async throws {
        let registered: Me = try await Api.shared.barePost("api/v1/auth/register", body: RegisterBody(invite: invite, login: login, password: password, displayName: displayName))
        _ = registered
        try await self.login(login: login, password: password)
    }

    /// POST /auth/device → одобряем свой же код текущей (парольной) сессией →
    /// поллинг /auth/device/token до минуты. Не вышло — nil, живём на cookie.
    static func upgradeToRefreshable() async -> TokenPair? {
        let api = Api.shared
        do {
            let begin: DeviceBegin = try await api.barePost("api/v1/auth/device", body: DeviceBeginBody(device: deviceKind))
            let approve = api.build("POST", "api/v1/auth/device/\(begin.userCode)/approve", query: [], body: nil, timeout: nil, bareClient: false)
            _ = try await api.send(approve, bareClient: false, retryAuth: false)
            return try await poll(begin: begin, deadline: Date().addingTimeInterval(60))
        } catch {
            LogUI.event("auth upgrade failed: \(error)")
            return nil
        }
    }

    /// Device-flow «с другой стороны»: ждем, пока человек впустит устройство.
    static func deviceLogin(onBegin: @escaping @MainActor (DeviceBegin) -> Void) async throws -> TokenPair {
        let begin: DeviceBegin = try await Api.shared.barePost("api/v1/auth/device", body: DeviceBeginBody(device: deviceKind))
        await onBegin(begin)
        return try await poll(begin: begin, deadline: expiry(begin))
    }

    /// Срок кода: expiresAt с сервера, иначе 10 минут.
    private static func expiry(_ begin: DeviceBegin) -> Date {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = f.date(from: begin.expiresAt) { return d }
        f.formatOptions = [.withInternetDateTime]
        return f.date(from: begin.expiresAt) ?? Date().addingTimeInterval(600)
    }

    private static func poll(begin: DeviceBegin, deadline: Date) async throws -> TokenPair {
        let api = Api.shared
        var interval = Double(max(begin.interval, 1))
        while Date() < deadline {
            try await Task.sleep(nanoseconds: UInt64(interval * 1_000_000_000))
            do {
                let pair: TokenPair = try await api.barePost("api/v1/auth/device/token", body: DevicePollBody(deviceCode: begin.deviceCode, cookie: false))
                return pair
            } catch let e as ApiError {
                switch e.code {
                case "authorization_pending":
                    continue
                case "slow_down":
                    interval = max(interval * 3 / 2, interval + 1)
                    continue
                case "access_denied", "expired_token", "invalid_grant":
                    throw e
                default:
                    // Сетевые ошибки не рвут ожидание — как на Android.
                    if e.offline, !Task.isCancelled { continue }
                    throw e
                }
            }
        }
        throw ApiError(status: 400, code: "expired_token", message: "Код истёк")
    }

    /// Выход: гасим серверную сессию и локальную.
    static func logout() async {
        try? await Api.shared.postNoBody("api/v1/auth/logout")
        Api.shared.clearSession()
    }
}

struct EmptyRsp: Codable {}
