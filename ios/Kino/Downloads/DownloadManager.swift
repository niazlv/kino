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
        /// Догрузка дорожек после видео: «озвучка 1 из 2», «субтитры».
        var stage: String? = nil

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
        /// Что догрузить в пакет после видео (TrackPlan.Plan.extras), по порядку.
        var extras: [String]? = nil
        /// Сколько озвучек догружается всего — для подписи «1 из N».
        var extraVoices: Int? = nil
        /// Видео с основной озвучкой готово и лежит в описи; дальше — догрузка.
        var entryReady: Bool? = nil
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
    /// Ключ задания → наблюдатель прогресса задачи.
    private var progressObservers: [String: NSKeyValueObservation] = [:]
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

    /// entryId задачи: из памяти, по адресу потока или — у догрузки — по
    /// скачанному пакету, в который она пишет.
    private func idFor(_ task: URLSessionTask) -> String? {
        if let hit = tasks.first(where: { $0.value == task })?.key { return hit }
        guard let at = task as? AVAssetDownloadTask else { return nil }
        return Self.key(for: at, in: metas)
    }

    private static func key(for task: AVAssetDownloadTask, in metas: [String: Meta]) -> String? {
        let url = task.urlAsset.url
        if let hit = metas.first(where: { $0.value.url == url.absoluteString })?.key { return hit }
        guard url.isFileURL else { return nil }
        let file = url.standardizedFileURL.path
        return metas.first(where: { m in
            m.value.location.map { LocalLibrary.url(for: $0).standardizedFileURL.path == file } ?? false
        })?.key
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
            // Видео с выбранной озвучкой; остальные озвучки и субтитры, как в
            // MKV на Android, — догрузкой в тот же пакет (TrackPlan.Plan). У
            // площадок, где студия — отдельный поток, озвучка одна.
            let plan = (try? await TrackPlan.plan(asset: asset, voiceTitle: voiceTitle)) ?? TrackPlan.Plan(primary: nil, extras: [])
            if let primary = plan.primary {
                config.primaryContentConfiguration.mediaSelections = [primary]
            }
            guard ServerConfig.generation == generation else { return }
            let task = session.makeAssetDownloadTask(downloadConfiguration: config)
            LogUI.event("download start id=\(entryId) extras=\(plan.extras)")
            tasks[entryId] = task
            watch(entryId, task)
            var all = metas
            all[entryId] = Meta(title: title, episodeLabel: episodeLabel, episodeId: episodeId,
                                voiceKey: voiceKey, voiceTitle: voiceTitle, url: url.absoluteString,
                                profile: profile, extras: plan.extras,
                                extraVoices: plan.extras.filter { $0 != TrackPlan.subtitlesExtra }.count)
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

    /// Прогресс — по NSProgress задачи (Apple велит его вместо отрезков
    /// времени делегата); стоит на нуле — считают отрезки (didProgress).
    private func watch(_ id: String, _ task: AVAssetDownloadTask) {
        progressObservers[id] = task.progress.observe(\.fractionCompleted, options: [.initial, .new]) { [weak self] p, _ in
            let percent = min(max(p.fractionCompleted, 0), 1) * 100
            Task { @MainActor in self?.didProgress(id, percent: percent) }
        }
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
        progressObservers[id] = nil
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

    /// Подхват незавершённых задач после перезапуска: по сохранённым адресам
    /// (догрузка — по пакету). Видео готово, а догрузка не начата (убили
    /// между заданиями) — начать.
    private func restore() {
        session.getAllTasks { [weak self] all in
            Task { @MainActor in
                guard let self else { return }
                let metas = self.metas
                for task in all {
                    guard let at = task as? AVAssetDownloadTask,
                          let id = Self.key(for: at, in: metas), let m = metas[id]
                    else { continue }
                    self.tasks[id] = at
                    self.watch(id, at)
                    if !self.allJobs.contains(where: { $0.id == id }) {
                        let state: Job.State = at.state == .suspended ? .paused : .running
                        let stage = m.entryReady == true ? m.extras?.first.map { Self.stageLabel(m, $0) } : nil
                        self.allJobs.append(Job(id: id, profile: m.profile, title: m.title, episodeLabel: m.episodeLabel,
                                                percent: 0, state: state, stage: stage))
                    }
                }
                for (id, m) in metas where m.entryReady == true && self.tasks[id] == nil {
                    Task { await self.startExtra(id) }
                }
            }
        }
    }

    private func update(_ id: String, percent: Double? = nil, state: Job.State? = nil, stage: String? = nil) {
        guard let i = allJobs.firstIndex(where: { $0.id == id }) else { return }
        if let percent { allJobs[i].percent = percent }
        if let state { allJobs[i].state = state }
        if let stage { allJobs[i].stage = stage }
    }

    private func forget(_ id: String) {
        tasks[id] = nil
        progressObservers[id] = nil
        loggedDecile[id] = nil
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
        if let m = metas[id], m.entryReady == true {
            // Догрузка пишет в тот же пакет; путь описи не трогаем.
            let same = m.location.map { LocalLibrary.url(for: $0).resolvingSymlinksInPath().path == location.resolvingSymlinksInPath().path } ?? false
            LogUI.event("download extra location id=\(id) same=\(same) uri=\(LocalLibrary.storedPath(location))")
            return
        }
        setMeta(id) { $0.location = LocalLibrary.storedPath(location) }
    }

    fileprivate func didComplete(_ task: URLSessionTask, error: Error?) {
        guard let id = idFor(task) else { return }
        if let meta = metas[id], meta.entryReady == true {
            didCompleteExtra(id, meta: meta, error: error)
            return
        }
        let meta = metas[id]
        let location = meta?.location.map { LocalLibrary.url(for: $0) }
        if let error {
            // Недокачанный пакет — не в опись: удалить.
            if let location { try? FileManager.default.removeItem(at: location) }
            let ns = error as NSError
            tasks[id] = nil
            progressObservers[id] = nil
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
            movie: meta.episodeLabel == "Фильм", voice: meta.voiceTitle
        )
        library.put(entry)
        LogUI.event("download done id=\(id) size=\(entry.size) uri=\(entry.uri)")
        // Видео уже смотрят; остальные озвучки и субтитры — догрузкой.
        if let extras = meta.extras, !extras.isEmpty {
            tasks[id] = nil
            progressObservers[id] = nil
            loggedDecile[id] = nil
            setMeta(id) { $0.entryReady = true }
            Task { await startExtra(id) }
            return
        }
        finishExtras(id, meta: meta)
    }

    // MARK: - Догрузка дорожек

    /// Следующая дорожка в скачанный пакет; кончились — задание готово.
    /// Пустой выбор (уже лежит, пропала из потока) пропускается.
    private func startExtra(_ id: String) async {
        while let meta = metas[id], let extra = meta.extras?.first, let loc = meta.location {
            let asset = AVURLAsset(url: LocalLibrary.url(for: loc))
            let selections = (try? await TrackPlan.extraSelections(asset: asset, extra: extra)) ?? []
            // Пока выбирали дорожку, задание отменили.
            guard metas[id] != nil else { return }
            if selections.isEmpty {
                setMeta(id) { $0.extras = Array(($0.extras ?? []).dropFirst()) }
                continue
            }
            let config = AVAssetDownloadConfiguration(asset: asset, title: meta.title)
            config.primaryContentConfiguration.mediaSelections = selections
            let task = session.makeAssetDownloadTask(downloadConfiguration: config)
            tasks[id] = task
            watch(id, task)
            let stage = Self.stageLabel(meta, extra)
            if allJobs.contains(where: { $0.id == id }) {
                update(id, percent: 0, state: .running, stage: stage)
            } else {
                allJobs.append(Job(id: id, profile: meta.profile, title: meta.title, episodeLabel: meta.episodeLabel,
                                   percent: 0, state: .running, stage: stage))
            }
            LogUI.event("download extra id=\(id) extra=\(extra) selections=\(selections.count)")
            task.resume()
            return
        }
        if let meta = metas[id] { finishExtras(id, meta: meta) }
    }

    /// Догрузка дорожки кончилась — удачно или нет, к следующей: сбой одной
    /// озвучки не повод трогать видео, оно уже в описи и играет.
    private func didCompleteExtra(_ id: String, meta: Meta, error: Error?) {
        tasks[id] = nil
        progressObservers[id] = nil
        loggedDecile[id] = nil
        if let ns = error as NSError? {
            if ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled {
                // «Отменить» во время догрузки: видео и успевшие дорожки остаются.
                finishExtras(id, meta: meta)
                return
            }
            LogUI.event("download extra failed id=\(id) extra=\(meta.extras?.first ?? "-") err=\(ns.domain) \(ns.code)")
        }
        setMeta(id) { $0.extras = Array(($0.extras ?? []).dropFirst()) }
        Task { await startExtra(id) }
    }

    /// Задание готово: размер в описи — с догруженными дорожками.
    private func finishExtras(_ id: String, meta: Meta) {
        if let loc = meta.location {
            let url = LocalLibrary.url(for: loc)
            let library = LocalLibrary.library(for: meta.profile)
            if var entry = library.entry(Self.entryId(fromKey: id)),
               entry.fileURL.standardizedFileURL == url.standardizedFileURL {
                entry.size = Self.dirSize(url)
                library.put(entry)
            }
            Task { LogUI.event("download tracks id=\(id) \(await Self.cachedTracks(url))") }
        }
        forget(id)
        allJobs.removeAll { $0.id == id }
    }

    /// «озвучка 1 из 2» / «субтитры».
    private static func stageLabel(_ meta: Meta, _ extra: String) -> String {
        if extra == TrackPlan.subtitlesExtra { return "субтитры" }
        let total = meta.extraVoices ?? 0
        let left = (meta.extras ?? []).filter { $0 != TrackPlan.subtitlesExtra }.count
        return "озвучка \(max(total - left + 1, 1)) из \(max(total, 1))"
    }

    fileprivate func didProgress(_ task: AVAssetDownloadTask, percent: Double) {
        guard let id = idFor(task) else { return }
        // NSProgress задачи считает все дорожки (watch), но бывает, что он
        // стоит на нуле, — тогда отрезки времени из делегата.
        let p = task.progress
        if p.fractionCompleted <= 0 { didProgress(id, percent: percent, fallback: true) }
        logProgress(id, ns: p.fractionCompleted * 100, time: percent, units: "\(p.completedUnitCount)/\(p.totalUnitCount)")
    }

    /// Журнал прогресса по десяткам процентов — сверять NSProgress и отрезки времени.
    private var loggedDecile: [String: Int] = [:]
    private func logProgress(_ id: String, ns: Double, time: Double, units: String) {
        let decile = Int(max(ns, time) / 10)
        guard decile != loggedDecile[id] else { return }
        loggedDecile[id] = decile
        LogUI.event(String(format: "download progress id=%@ ns=%.1f%% time=%.1f%% units=%@", id, ns, time, units))
    }

    private func didProgress(_ id: String, percent: Double, fallback: Bool = false) {
        // Пошёл NSProgress — запасные отрезки времени ему не мешают.
        if !fallback, percent <= 0 { return }
        guard tasks[id] != nil, let job = allJobs.first(where: { $0.id == id }) else { return }
        // Не дёргать экран на каждую сотую процента.
        guard abs(job.percent - percent) >= 0.5 || (percent >= 100 && job.percent < 100) else { return }
        update(id, percent: percent)
    }

    /// Сколько озвучек и субтитров легло в пакет — для журнала.
    private static func cachedTracks(_ location: URL) async -> String {
        let asset = AVURLAsset(url: location)
        guard let cache = asset.assetCache else { return "нет assetCache" }
        var out: [String] = []
        for (label, kind) in [("audio", AVMediaCharacteristic.audible), ("subs", .legible)] {
            guard let group = try? await asset.loadMediaSelectionGroup(for: kind) else { continue }
            let names = await TrackPlan.options(in: group, among: cache.mediaSelectionOptions(in: group)).map(\.name)
            out.append("\(label)=\(names.count)\(names)")
        }
        return out.joined(separator: " ")
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
