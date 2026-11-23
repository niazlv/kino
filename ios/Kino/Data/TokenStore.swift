import Foundation
import Security

/// Пара токенов в Keychain (сервис ru.sorewa.kino) + deviceId и настройки в UserDefaults.
/// Зеркало data/TokenStore.kt, только токены — в Keychain, а не в открытом DataStore.
///
/// Сессия у каждого сервера своя (см. ServerConfig): сервер по умолчанию —
/// прежняя запись «session», другой — «session-<профиль>». Без явного
/// профиля — выбранный сейчас сервер. Настройки и deviceId — общие.
enum TokenStore {
    private static let service = "ru.sorewa.kino"
    private static let defaults = UserDefaults.standard

    /// Запись Keychain для профиля сервера; nil — сервер по умолчанию.
    static func account(for profile: String?) -> String {
        profile.map { "session-\($0)" } ?? "session"
    }

    struct Settings: Codable {
        var maxQuality: Int = 0        // 0 = авто, 1080/1440/2160
        var skipMode: String = "auto"  // auto | button | off
        var autonext: Bool = true
        var hideAfter: Int = 5         // сек; 0 = никогда
        var swipes: Bool = true        // яркость/громкость свайпами
        var statsAlways: Bool = false
        var hevc: String = "self"      // self | declare | off
        var av1: String = "self"
        var wifiOnly: Bool = true
    }

    // MARK: - Ключчейн

    static func save(_ pair: TokenPair, profile: String? = ServerConfig.profile) {
        let data = try? JSONEncoder().encode(pair)
        guard let data else { return }
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account(for: profile),
        ]
        let update: [String: Any] = [kSecValueData as String: data]
        let status = SecItemUpdate(query as CFDictionary, update as CFDictionary)
        if status == errSecItemNotFound {
            query[kSecValueData as String] = data
            query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
            SecItemAdd(query as CFDictionary, nil)
        }
    }

    static func load(profile: String? = ServerConfig.profile) -> TokenPair? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account(for: profile),
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data else { return nil }
        return try? JSONDecoder().decode(TokenPair.self, from: data)
    }

    static func refreshToken(profile: String? = ServerConfig.profile) -> String? { load(profile: profile)?.refreshToken }

    static func clear(profile: String? = ServerConfig.profile) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account(for: profile),
        ]
        SecItemDelete(query as CFDictionary)
    }

    // MARK: - deviceId

    /// Локальный UUID устройства — генерируется при первом обращении.
    static var deviceId: String {
        if let id = defaults.string(forKey: "device_id") { return id }
        let id = UUID().uuidString
        defaults.set(id, forKey: "device_id")
        return id
    }

    // MARK: - Настройки

    static var settings: Settings {
        get {
            if let data = defaults.data(forKey: "settings"),
               let s = try? JSONDecoder().decode(Settings.self, from: data) { return s }
            return Settings()
        }
        set {
            if let data = try? JSONEncoder().encode(newValue) {
                defaults.set(data, forKey: "settings")
            }
        }
    }

    static func updateSettings(_ mutate: (inout Settings) -> Void) {
        var s = settings
        mutate(&s)
        settings = s
    }
}
