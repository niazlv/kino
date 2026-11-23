import UIKit

/// Загрузчик артов с сессией: память + диск. Coil на Android использует тот же
/// OkHttp-клиент с cookie — здесь свой URLSession с заголовком kino_session.
@MainActor
final class ArtStore: ObservableObject {
    static let shared = ArtStore()

    private let memory = NSCache<NSString, UIImage>()
    private var dir: URL
    private var inFlight: [String: Task<UIImage?, Never>] = [:]

    /// Кэш на диске — свой у каждого сервера: путь «titles/5/art» у разных
    /// серверов — разные тайтлы. nil — сервер по умолчанию (прежняя папка).
    nonisolated static func dir(profile: String?) -> URL {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
        return caches.appendingPathComponent(profile.map { "art-\($0)" } ?? "art", isDirectory: true)
    }

    private init() {
        dir = Self.dir(profile: ServerConfig.profile)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        memory.countLimit = 400
    }

    /// Сервер сменён: память — пустая, диск — папка его профиля.
    func reload() {
        memory.removeAllObjects()
        inFlight = [:]
        dir = Self.dir(profile: ServerConfig.profile)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    func cached(_ path: String) -> UIImage? {
        memory.object(forKey: path as NSString)
    }

    /// Постер тайтла: GET /titles/{id}/art.
    func titleArt(_ titleId: Int64) async -> UIImage? {
        await load("api/v1/titles/\(titleId)/art")
    }

    /// Кадр серии: GET /episodes/{id}/art.
    func episodeArt(_ episodeId: Int64) async -> UIImage? {
        await load("api/v1/episodes/\(episodeId)/art")
    }

    private func load(_ path: String) async -> UIImage? {
        if let hit = memory.object(forKey: path as NSString) { return hit }
        let file = dir.appendingPathComponent(path.replacingOccurrences(of: "/", with: "_"))
        if let img = UIImage(contentsOfFile: file.path) {
            memory.setObject(img, forKey: path as NSString)
            return img
        }
        if let running = inFlight[path] { return await running.value }
        // Картинка, запрошенная при прежнем сервере, в кэш нового не ложится.
        let generation = ServerConfig.generation
        let task = Task<UIImage?, Never> { [weak self] in
            guard let self else { return nil }
            defer { Task { self.clearInFlight(path) } }
            guard let data = try? await Api.shared.artData(path) else { return nil }
            guard let img = UIImage(data: data) else { return nil }
            guard ServerConfig.generation == generation else { return img }
            try? data.write(to: file, options: .atomic)
            await MainActor.run { self.memory.setObject(img, forKey: path as NSString) }
            return img
        }
        inFlight[path] = task
        return await task.value
    }

    private func clearInFlight(_ path: String) { inFlight[path] = nil }
}
