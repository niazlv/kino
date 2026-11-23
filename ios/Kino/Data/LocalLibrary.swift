import Foundation

/// Опись скачанного — порт data/LocalLibrary.kt: один JSON-файл,
/// атомарная запись, локальный прогресс переживает перекачку.
///
/// У каждого сервера своя опись (id тайтлов и серий у них разные, см.
/// ServerConfig): `shared` смотрит в опись выбранного сервера и при смене
/// сервера перечитывается (reload). Сами пакеты .movpkg лежат там, куда их
/// положила система, — общие, но каждый записан ровно в одну опись.
@MainActor
final class LocalLibrary: ObservableObject {
    static let shared = LocalLibrary()

    /// Файл описи профиля; nil — сервер по умолчанию (прежнее место, как было).
    nonisolated static func fileURL(profile: String?) -> URL {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        guard let profile else { return support.appendingPathComponent("library.json") }
        return support.appendingPathComponent("servers", isDirectory: true)
            .appendingPathComponent(profile, isDirectory: true)
            .appendingPathComponent("library.json")
    }

    /// Опись профиля: для выбранного сервера — `shared`, для другого — своя
    /// копия с диска (загрузка, начатая при прежнем сервере, докачалась после
    /// смены — запись должна лечь в опись того сервера).
    static func library(for profile: String?) -> LocalLibrary {
        profile == ServerConfig.profile ? shared : LocalLibrary(file: fileURL(profile: profile))
    }

    struct Entry: Codable, Identifiable, Equatable {
        /// "titleId:season:episode" или "f<hash>:s:e" для чужих файлов.
        var id: String
        var titleId: Int64
        var title: String
        var season: Int
        var episode: Int
        var uri: String
        var size: Int64
        var addedAt: Int64
        var positionMs: Int64
        var durationMs: Int64
        var watched: Bool
        var dirty: Bool
        var movie: Bool
        /// Озвучка, выбранная при загрузке: с неё начинает плеер, пока
        /// человек не выбрал в тайтле другую дорожку файла.
        var voice: String? = nil

        /// Файл на диске. В uri — путь относительно домашней папки приложения:
        /// абсолютный путь контейнера меняется при обновлении/переустановке.
        var fileURL: URL { LocalLibrary.url(for: uri) }
    }

    /// Относительный путь → URL; старый абсолютный из прежнего контейнера
    /// перевешивается на текущую домашнюю папку.
    nonisolated static func url(for uri: String) -> URL {
        // Маска симулятора перед путями вне видимой песочницы.
        let uri = uri.hasPrefix("/.nofollow/") ? String(uri.dropFirst("/.nofollow".count)) : uri
        let home = URL(fileURLWithPath: NSHomeDirectory(), isDirectory: true)
        guard uri.hasPrefix("/") else { return home.appendingPathComponent(uri) }
        if FileManager.default.fileExists(atPath: uri) { return URL(fileURLWithPath: uri) }
        if let r = uri.range(of: "/Library/") {
            return home.appendingPathComponent(String(uri[uri.index(after: r.lowerBound)...]))
        }
        return URL(fileURLWithPath: uri)
    }

    /// URL файла → путь для хранения (относительно домашней папки).
    nonisolated static func storedPath(_ url: URL) -> String {
        var raw = url.standardizedFileURL.path
        if raw.hasPrefix("/.nofollow/") { raw = String(raw.dropFirst("/.nofollow".count)) }
        let home = URL(fileURLWithPath: NSHomeDirectory(), isDirectory: true).standardizedFileURL.path
        let path = raw
        if path.hasPrefix(home + "/") { return String(path.dropFirst(home.count + 1)) }
        // /private/var и /var — один и тот же путь.
        let alt = path.hasPrefix("/private") ? String(path.dropFirst("/private".count)) : "/private" + path
        if alt.hasPrefix(home + "/") { return String(alt.dropFirst(home.count + 1)) }
        return path
    }

    @Published private(set) var entries: [Entry] = []

    private var file: URL

    private convenience init() {
        self.init(file: Self.fileURL(profile: ServerConfig.profile))
    }

    init(file: URL) {
        self.file = file
        try? FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        load()
    }

    /// Сервер сменён — перечитать опись его профиля.
    func reload() {
        file = Self.fileURL(profile: ServerConfig.profile)
        try? FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        entries = []
        load()
    }

    private func load() {
        guard let data = try? Data(contentsOf: file) else { return }
        if let list = try? JSONDecoder().decode([Entry].self, from: data) {
            entries = list
        } else {
            // Неразборчивый файл — бэкап и старт с пустого.
            try? FileManager.default.copyItem(at: file, to: file.appendingPathExtension("unreadable"))
            entries = []
        }
        // Абсолютные пути прежних версий — в относительные; записи с
        // исчезнувшими файлами — вон.
        var changed = false
        entries = entries.compactMap { e in
            let url = e.fileURL
            guard FileManager.default.fileExists(atPath: url.path) else { changed = true; return nil }
            var e = e
            let stored = Self.storedPath(url)
            if stored != e.uri { e.uri = stored; changed = true }
            return e
        }
        if changed { save() }
    }

    private func save() {
        guard let data = try? JSONEncoder().encode(entries) else { return }
        let tmp = file.appendingPathExtension("tmp")
        do {
            try data.write(to: tmp, options: .atomic)
            _ = try FileManager.default.replaceItemAt(file, withItemAt: tmp)
        } catch {
            try? data.write(to: file, options: .atomic)
        }
    }

    // MARK: - Записи

    func entry(_ id: String) -> Entry? {
        entries.first { $0.id == id }
    }

    func entry(episodeId: Int64) -> Entry? {
        entries.first { $0.id.hasSuffix(":\(episodeId)") || $0.id == "e\(episodeId)" }
    }

    /// Добавить/обновить запись; старый прогресс и watched сохраняются.
    func put(_ entry: Entry) {
        if let old = self.entry(entry.id) {
            var e = entry
            e.positionMs = old.positionMs
            e.durationMs = old.durationMs
            e.watched = old.watched
            e.dirty = old.dirty
            e.addedAt = old.addedAt
            entries.removeAll { $0.id == entry.id }
            entries.append(e)
        } else {
            entries.append(entry)
        }
        save()
    }

    func remove(_ id: String) {
        if let e = entry(id) {
            try? FileManager.default.removeItem(at: e.fileURL)
        }
        entries.removeAll { $0.id == id }
        save()
    }

    func wipeWatched() {
        for e in entries where e.watched {
            try? FileManager.default.removeItem(at: e.fileURL)
        }
        entries.removeAll { $0.watched }
        save()
    }

    func setProgress(_ id: String, positionMs: Int64, durationMs: Int64, watched: Bool) {
        guard let idx = entries.firstIndex(where: { $0.id == id }) else { return }
        entries[idx].positionMs = positionMs
        // Длину не затираем нулём, «просмотрено» только ставится (LocalLibrary.kt):
        // пересмотр досмотренного не снимает отметку.
        if durationMs > 0 { entries[idx].durationMs = durationMs }
        entries[idx].watched = entries[idx].watched || watched
        entries[idx].dirty = true
        save()
    }

    /// Отметка руками («Просмотрено» / «Не просмотрено» в загрузках).
    func setWatched(_ id: String, _ watched: Bool) {
        guard let idx = entries.firstIndex(where: { $0.id == id }) else { return }
        entries[idx].watched = watched
        if !watched { entries[idx].positionMs = 0 }
        entries[idx].dirty = true
        save()
    }

    func markSynced(_ id: String) {
        guard let idx = entries.firstIndex(where: { $0.id == id }) else { return }
        entries[idx].dirty = false
        save()
    }

    /// Привязать безымянную запись к каталогу (резолв через suggest).
    func retitle(id: String, titleId: Int64) {
        guard let idx = entries.firstIndex(where: { $0.id == id }) else { return }
        entries[idx].titleId = titleId
        entries[idx].id = Self.entryId(titleId: titleId, season: entries[idx].season, episode: entries[idx].episode)
        save()
    }

    var dirtyEntries: [Entry] { entries.filter(\.dirty) }

    var bytes: Int64 { entries.reduce(0) { $0 + $1.size } }

    func episodesOfTitle(_ titleId: Int64) -> [Entry] {
        entries.filter { $0.titleId == titleId }
    }

    static func entryId(titleId: Int64, season: Int, episode: Int) -> String {
        "\(titleId):\(season):\(episode)"
    }
}
