import AVFoundation
import Foundation

/// Нативная качалка: AVAssetDownloadURLSession качает HLS шлюза в .movpkg.
/// Задачи живут в фоне; после перезапуска подхватываются из сессии.
///
/// По правилам AVAssetDownloadTask (и образцу Apple HLSCatalog) пакет
/// остаётся там, куда его положила система; в описи — путь относительно
/// домашней папки. Готовым он становится только в didCompleteWithError без
/// ошибки: didFinishDownloadingTo приходит и для отменённых/сорванных.
///
/// Фоновая сессия одна на все серверы (см. ServerConfig): задание помнит свой
/// профиль и докачанное ложится в опись своего сервера, даже если его успели
/// сменить. Ключ задания — entryId с профилем («s-1a2b3c4d|5:1:2»): id серий у
/// серверов совпадают числами, а значат разное. У сервера по умолчанию ключ —
/// просто entryId, как в прежних версиях (их задания подхватываются как есть).
@MainActor
final class DownloadManager: NSObject, ObservableObject {
    static let shared = DownloadManager()

    struct Job: Identifiable, Equatable {
        var id: String            // ключ задания: entryId с профилем (см. key)
        var profile: String?      // сервер задания; nil — по умолчанию
        var title: String
        var episodeLabel: String  // "Фильм" | "S сезон, N серия"
        var percent: Double
        var state: State

        enum State: Equatable {
            case running, paused, done, failed(String)
        }
    }

    /// Что нужно, чтобы показать задачу после перезапуска и повторить её.
    struct Meta: Codable {
        var title: String
        var episodeLabel: String
        var episodeId: Int64
        var voiceKey: String?
        var voiceTitle: String?
        var url: String
        /// Куда система положила пакет (относительно дома), до завершения.
        var location: String?
        /// Профиль сервера задания; nil — по умолчанию (и все задания прежних версий).
        var profile: String?
    }

    /// Ключ задания: у сервера по умолчанию — сам entryId.
    nonisolated static func key(entryId: String, profile: String?) -> String {
        profile.map { "\($0)|\(entryId)" } ?? entryId
    }

    /// entryId из ключа задания.
    nonisolated static func entryId(fromKey key: String) -> String {
        key.split(separator: "|", maxSplits: 1).last.map(String.init) ?? key
    }

    /// Все задания всех серверов.
    @Published private(set) var allJobs: [Job] = []
    /// Задания выбранного сервера — их и показывают «Загрузки».
    var jobs: [Job] { allJobs.filter { $0.profile == ServerConfig.profile } }
    /// Последняя ошибка — для панели «Последняя загрузка не удалась».
    @Published private(set) var lastError: String?

    private var session: AVAssetDownloadURLSession!
    /// Ключ задания → задача (в памяти).
    private var tasks: [String: AVAssetDownloadTask] = [:]
    /// Обработчик фоновой сессии от системы (handleEventsForBackgroundURLSession).
    var backgroundCompletion: (() -> Void)?

    static let sessionId = "ru.sorewa.kino.downloads"

    override private init() {
        super.init()
        let cfg = URLSessionConfiguration.background(withIdentifier: Self.sessionId)
        cfg.isDiscretionary = false
        cfg.sessionSendsLaunchEvents = true
        // Колбэки — последовательно на главной очереди: URLSession ждёт
        // последовательную очередь, иначе ошибка могла прийти раньше конца.
        session = AVAssetDownloadURLSession(
            configuration: cfg,
            assetDownloadDelegate: self,
            delegateQueue: .main
        )
        restore()
    }

    // MARK: - Метаданные задач

    private var metas: [String: Meta] {
        get {
            guard let data = UserDefaults.standard.data(forKey: "dl_meta"),
                  let m = try? JSONDecoder().decode([String: Meta].self, from: data) else { return [:] }
            return m
        }
        set {
            if let data = try? JSONEncoder().encode(newValue) {
                UserDefaults.standard.set(data, forKey: "dl_meta")
            }
        }
    }

    private func setMeta(_ id: String, _ change: (inout Meta) -> Void) {
        var all = metas
        guard var m = all[id] else { return }
        change(&m)
        all[id] = m
        metas = all
    }

    /// entryId задачи: из памяти или по адресу потока.
    private func idFor(_ task: URLSessionTask) -> String? {
        if let hit = tasks.first(where: { $0.value == task })?.key { return hit }
        guard let at = task as? AVAssetDownloadTask else { return nil }
        let url = at.urlAsset.url.absoluteString
        return metas.first(where: { $0.value.url == url })?.key
    }

    // MARK: - Постановка задачи

    /// Скачивание серии: POST /playback → master.m3u8 → AVAssetDownloadTask.
    func enqueue(entryId: String, title: String, episodeLabel: String, episodeId: Int64, voice: VoiceOption?) async {
        await start(key: Self.key(entryId: entryId, profile: ServerConfig.profile), title: title,
                    episodeLabel: episodeLabel, episodeId: episodeId,
                    voiceKey: voice?.key, voiceTitle: voice?.title)
    }

    private func start(key entryId: String, title: String, episodeLabel: String, episodeId: Int64, voiceKey: String?, voiceTitle: String?) async {
        // Задание — для сервера, выбранного сейчас; сменили, пока выпускался
        // тикет, — не начинаем: поток и опись были бы от разных серверов.
        let profile = ServerConfig.profile
        let generation = ServerConfig.generation
        if let running = tasks[entryId] {
            // Уже качается — не плодить вторую задачу на тот же файл.
            if running.state == .running || running.state == .suspended { return }
        }
        do {
            var choice = PlaybackChoice()
            choice.voice = voiceKey
            choice.variantLabel = voiceTitle
            let pb = try await Api.shared.playback(episodeId, choice: choice)
            guard ServerConfig.generation == generation else { return }
            guard let url = Api.absolute(pb.url) else {
                lastError = "Плохой адрес потока"
                return
            }
            let asset = AVURLAsset(url: url)
            let config = AVAssetDownloadConfiguration(asset: asset, title: title)
            // Выбранная озвучка — через mediaSelections.
            if let voiceTitle, let selection = try? await Self.selection(asset: asset, voiceTitle: voiceTitle) {
                config.primaryContentConfiguration.mediaSelections = [selection]
            }
            guard ServerConfig.generation == generation else { return }
            let task = session.makeAssetDownloadTask(downloadConfiguration: config)
            tasks[entryId] = task
            var all = metas
            all[entryId] = Meta(title: title, episodeLabel: episodeLabel, episodeId: episodeId,
                                voiceKey: voiceKey, voiceTitle: voiceTitle, url: url.absoluteString,
                                profile: profile)
            metas = all
            allJobs.removeAll { $0.id == entryId }
            allJobs.append(Job(id: entryId, profile: profile, title: title, episodeLabel: episodeLabel, percent: 0, state: .running))
            lastError = nil
            task.resume()
        } catch let e as ApiError {
            lastError = e.offline ? "Нет связи с сервером" : "Не получилось открыть поток"
        } catch {
            lastError = "Не получилось начать загрузку"
        }
    }

    /// AVMediaSelection с выбранной озвучкой: копия базового выбора + подмена аудио.
    static func selection(asset: AVURLAsset, voiceTitle: String) async throws -> AVMediaSelection? {
        let base = try await asset.load(.preferredMediaSelection)
        guard let group = try await asset.loadMediaSelectionGroup(for: .audible) else { return nil }
        // По NAME дорожки: displayName у всех — просто «Russian».
        var opt: AVMediaSelectionOption?
        for o in group.options where await o.trackName().localizedCaseInsensitiveContains(voiceTitle) {
            opt = o
            break
        }
        guard let opt else { return nil }
        let mutable = base.mutableCopy() as! AVMutableMediaSelection
        mutable.select(opt, in: group)
        return mutable
    }

    /// Отмена: недокачанный пакет уберёт didCompleteWithError (-999).
    func cancel(_ id: String) {
        if let task = tasks[id] {
            task.cancel()
        } else {
            forget(id)
        }
        allJobs.removeAll { $0.id == id }
    }

    func pause(_ id: String) {
        tasks[id]?.suspend()
        update(id, state: .paused)
    }

    func resume(_ id: String) {
        tasks[id]?.resume()
        update(id, state: .running)
    }

    /// Сорванную — заново, свежим тикетом.
    func retry(_ id: String) {
        // Повтор выпускает тикет у выбранного сервера — только его заданиям.
        guard let m = metas[id], m.profile == ServerConfig.profile else { return }
        tasks[id] = nil
        Task {
            await start(key: id, title: m.title, episodeLabel: m.episodeLabel, episodeId: m.episodeId,
                        voiceKey: m.voiceKey, voiceTitle: m.voiceTitle)
        }
    }

    func clearError() { lastError = nil }

    /// Сервер сменён: «Загрузки» показывают задания нового (задания прежнего
    /// докачиваются в фоне в его опись), ошибка прежнего — не про этот сервер.
    func serverChanged() {
        lastError = nil
        objectWillChange.send()
    }

    /// Подхват незавершённых задач после перезапуска: по сохранённым адресам.
    private func restore() {
        session.getAllTasks { [weak self] all in
            Task { @MainActor in
                guard let self else { return }
                let metas = self.metas
                for task in all {
                    guard let at = task as? AVAssetDownloadTask,
                          let (id, m) = metas.first(where: { $0.value.url == at.urlAsset.url.absoluteString }).map({ ($0.key, $0.value) })
                    else { continue }
                    self.tasks[id] = at
                    if !self.allJobs.contains(where: { $0.id == id }) {
                        let state: Job.State = at.state == .suspended ? .paused : .running
                        self.allJobs.append(Job(id: id, profile: m.profile, title: m.title, episodeLabel: m.episodeLabel, percent: 0, state: state))
                    }
                }
            }
        }
    }

    private func update(_ id: String, percent: Double? = nil, state: Job.State? = nil) {
        guard let i = allJobs.firstIndex(where: { $0.id == id }) else { return }
        if let percent { allJobs[i].percent = percent }
        if let state { allJobs[i].state = state }
    }

    private func forget(_ id: String) {
        tasks[id] = nil
        var all = metas
        all[id] = nil
        metas = all
    }

    static func dirSize(_ url: URL) -> Int64 {
        let fm = FileManager.default
        var size: Int64 = 0
        if let walker = fm.enumerator(at: url, includingPropertiesForKeys: [.fileSizeKey]) {
            for case let file as URL in walker {
                size += Int64((try? file.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0)
            }
        }
        return size
    }

    // MARK: - События делегата (главная очередь)

    fileprivate func didFinish(_ task: AVAssetDownloadTask, location: URL) {
        guard let id = idFor(task) else { return }
        setMeta(id) { $0.location = LocalLibrary.storedPath(location) }
    }

    fileprivate func didComplete(_ task: URLSessionTask, error: Error?) {
        guard let id = idFor(task) else { return }
        let meta = metas[id]
        let location = meta?.location.map { LocalLibrary.url(for: $0) }
        if let error {
            // Недокачанный пакет — не в опись: удалить.
            if let location { try? FileManager.default.removeItem(at: location) }
            let ns = error as NSError
            tasks[id] = nil
            setMeta(id) { $0.location = nil }
            if ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled {
                forget(id)
                allJobs.removeAll { $0.id == id }
                return
            }
            update(id, state: .failed(ns.localizedDescription))
            if meta?.profile == ServerConfig.profile { lastError = ns.localizedDescription }
            return
        }
        guard let location, let meta else {
            update(id, state: .failed("Файл не найден"))
            return
        }
        var url = location
        var rv = URLResourceValues()
        rv.isExcludedFromBackup = true
        try? url.setResourceValues(rv)
        let entryId = Self.entryId(fromKey: id)
        let parts = entryId.split(separator: ":").compactMap { Int64($0) }
        let titleId = parts.count == 3 ? parts[0] : 0
        let season = parts.count == 3 ? Int(parts[1]) : 0
        let episode = parts.count == 3 ? Int(parts[2]) : 0
        // Опись сервера задания, а не выбранного сейчас.
        let library = LocalLibrary.library(for: meta.profile)
        // Прежний файл той же серии (перекачка) — заменить.
        if let old = library.entry(entryId), old.fileURL.standardizedFileURL != location.standardizedFileURL {
            try? FileManager.default.removeItem(at: old.fileURL)
        }
        let entry = LocalLibrary.Entry(
            id: entryId, titleId: titleId, title: meta.title,
            season: season, episode: episode,
            uri: LocalLibrary.storedPath(location), size: Self.dirSize(location),
            addedAt: Int64(Date().timeIntervalSince1970 * 1000),
            positionMs: 0, durationMs: 0, watched: false, dirty: false,
            movie: meta.episodeLabel == "Фильм"
        )
        library.put(entry)
        LogUI.event("download done id=\(id) size=\(entry.size) uri=\(entry.uri)")
        forget(id)
        allJobs.removeAll { $0.id == id }
    }

    fileprivate func didProgress(_ task: AVAssetDownloadTask, percent: Double) {
        guard let id = idFor(task) else { return }
        update(id, percent: percent)
    }

    fileprivate func didFinishEvents() {
        backgroundCompletion?()
        backgroundCompletion = nil
    }
}

// MARK: - Делегат загрузки

extension DownloadManager: AVAssetDownloadDelegate {

    nonisolated func urlSession(_ session: URLSession, assetDownloadTask: AVAssetDownloadTask, didFinishDownloadingTo location: URL) {
        MainActor.assumeIsolated { self.didFinish(assetDownloadTask, location: location) }
    }

    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        MainActor.assumeIsolated { self.didComplete(task, error: error) }
    }

    nonisolated func urlSession(_ session: URLSession, assetDownloadTask: AVAssetDownloadTask, didLoad timeRange: CMTimeRange, totalTimeRangesLoaded loadedTimeRanges: [NSValue], timeRangeExpectedToLoad: CMTimeRange) {
        var loaded: Double = 0
        for v in loadedTimeRanges {
            loaded += v.timeRangeValue.duration.seconds
        }
        let expected = max(timeRangeExpectedToLoad.duration.seconds, 0.001)
        let percent = min(loaded / expected * 100, 100)
        MainActor.assumeIsolated { self.didProgress(assetDownloadTask, percent: percent) }
    }

    nonisolated func urlSessionDidFinishEvents(forBackgroundURLSession session: URLSession) {
        MainActor.assumeIsolated { self.didFinishEvents() }
    }
}
