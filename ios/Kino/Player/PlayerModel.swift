import AVFoundation
import Combine
import MediaPlayer
import UIKit

/// Модель плеера: выпуск тикета, AVPlayer, дорожки, скипы, прогресс,
/// следующая/предыдущая серия — порт PlaybackClient + PlaybackUiState.
@MainActor
@Observable
final class PlayerModel {
    enum Phase: Equatable {
        case preparing, playing, paused, buffering, ended, failed(String)
    }

    // Вход
    let episodeId: Int64
    let titleId: Int64
    var autoplay: Bool
    /// Локальный файл из библиотеки (офлайн-режим).
    var localEntry: LocalLibrary.Entry?

    // Воспроизведение
    let player = AVPlayer()
    private(set) var phase: Phase = .preparing
    private(set) var playback: Playback?
    private(set) var detail: TitleDetail?
    private(set) var duration: Double = 0
    var position: Double = 0
    var buffered: Double = 0

    // Выбор
    var speed: Float = 1
    var voiceOptions: [VoiceOption] = []
    /// Озвучки, лежащие в скачанном пакете (имена из плейлиста); у потока пусто.
    var localAudio: [String] = []
    var subOptions: [String] = []
    var qualityMax: Int = 0          // 0 = из настроек
    var audioLabel: String = ""
    var subLabel: String = "Выкл"
    private var choice = PlaybackChoice()
    var choiceVoice: String? { choice.voice }
    /// Куда встать, когда новый поток готов: сохранённый прогресс или момент
    /// подмены (качество, озвучка, восстановление).
    private var pendingSeek: Double?
    /// Человек хочет, чтобы шло (а не пауза): отличает буферизацию от паузы.
    private var wantsPlay = true

    // Скипы
    private(set) var skips: [SkipSegment] = []
    private var rawSkips: [SkipSegment] = []
    /// Для плашки «Заставка пропущена / Вернуть».
    var autoSkippedSegment: SkipSegment?
    private var autoSkippedAt: Date?
    /// Автопропуск — один раз на отрезок за серию: «Вернуть» и перемотка назад
    /// не выкидывают снова (как Android).
    private var autoSkipped: Set<String> = []

    // Панель
    var panelVisible = false
    var statsVisible = false
    var lastInput = Date()
    var menu: MenuKind?

    enum MenuKind: Equatable {
        case video, audio, subs, speed
    }

    // Прогресс
    private var reportCount = 0
    private var reportedPosition: Int64 = -1
    private var timeObserver: Any?
    private var itemObservers: [NSObjectProtocol] = []
    private var appObservers: [NSObjectProtocol] = []
    private var cancellables: Set<AnyCancellable> = []
    private var itemCancellables: Set<AnyCancellable> = []

    // Восстановление потока
    private var recoverAttempts: [Date] = []
    private var refreshedLink = false

    // Жизненный цикл
    private var startTask: Task<Void, Never>?
    private var issueTask: Task<Void, Never>?
    private var stopped = false

    // Соседи по сериалу
    private(set) var prevEpisode: Episode?
    private(set) var nextEpisode: Episode?
    var confirmNext = false

    var settings: TokenStore.Settings { TokenStore.settings }

    weak var app: AppState?

    init(episodeId: Int64, titleId: Int64, autoplay: Bool) {
        self.episodeId = episodeId
        self.titleId = titleId
        self.autoplay = autoplay
        self.wantsPlay = autoplay
        #if DEBUG
        // Прогоны проверок — без звука (SIMCTL_CHILD_KINO_MUTE=1).
        if ProcessInfo.processInfo.environment["KINO_MUTE"] != nil { player.isMuted = true }
        #endif
    }

    /// Локальный файл: без тикетов, прогресс — в библиотеку (dirty).
    convenience init(entry: LocalLibrary.Entry) {
        self.init(episodeId: 0, titleId: entry.titleId, autoplay: true)
        localEntry = entry
    }

    // MARK: - Запуск

    /// Запуск с ручкой отмены: закрытый до выдачи тикета плеер не должен
    /// потом заиграть без экрана.
    func begin() {
        startTask = Task { await start() }
    }

    private func start() async {
        configureAudioSession()
        startObservers()
        if let entry = localEntry {
            await openLocal(entry)
            return
        }
        // Тайтл: подписи серий, соседи и запомненный выбор (как PlaybackClient.start).
        if let d = try? await Api.shared.title(titleId) {
            guard !stopped else { return }
            detail = d
            let eps = d.episodes.sorted { ($0.seasonNo, $0.episodeNo) < ($1.seasonNo, $1.episodeNo) }
            if let idx = eps.firstIndex(where: { $0.id == episodeId }) {
                prevEpisode = idx > 0 ? eps[idx - 1] : nil
                nextEpisode = idx + 1 < eps.count ? eps[idx + 1] : nil
            }
            let src = d.prefs?.source ?? ""
            let audio = d.prefs?.audio ?? ""
            choice = PlaybackChoice(
                source: src.isEmpty ? nil : src,
                variantLabel: audio.isEmpty ? nil : audio
            )
            if !audio.isEmpty { audioLabel = audio }
            if let sub = d.prefs?.subtitle, !sub.isEmpty { subLabel = sub }
        }
        // Позиция — до тикета: досмотренное (completed) — с нуля.
        if let saved = try? await Api.shared.progress(episodeId) {
            if saved.status != "completed" && saved.positionMs > 0 {
                pendingSeek = Double(saved.positionMs) / 1000
            }
        }
        if let list = try? await Api.shared.voices(episodeId) {
            voiceOptions = list.voices
        }
        guard !stopped, !Task.isCancelled else { return }
        await issue()
    }

    private func openLocal(_ entry: LocalLibrary.Entry) async {
        LogUI.event("local open id=\(entry.id) file=\(entry.fileURL.lastPathComponent) exists=\(FileManager.default.fileExists(atPath: entry.fileURL.path))")
        let item = AVPlayerItem(url: entry.fileURL)
        // Дорожки: выбранная в этом тайтле без сети, иначе — та, что
        // выбрали при загрузке. Сервер не спрашиваем: его может не быть.
        if let audio = Self.localPref("audio", titleId) ?? entry.voice, !audio.isEmpty { audioLabel = audio }
        if let sub = Self.localPref("sub", titleId) { subLabel = sub }
        if entry.positionMs > 0 && !entry.watched {
            pendingSeek = Double(entry.positionMs) / 1000
        }
        duration = entry.durationMs > 0 ? Double(entry.durationMs) / 1000 : 0
        await attach(item, asset: item.asset)
    }

    /// Выпуск тикета с текущим выбором. Поток уже шёл — новый встанет на тот
    /// же момент (Android swapTo), а не с нуля поверх прогресса на сервере.
    func issue() async {
        if localEntry != nil {
            if let entry = localEntry { await openLocal(entry) }
            return
        }
        if playback != nil, position > 1 { pendingSeek = position }
        phase = .preparing
        do {
            let pb = try await Api.shared.playback(episodeId, choice: choice, maxHeight: qualityMax > 0 ? qualityMax : nil)
            guard !stopped, !Task.isCancelled else { return }
            playback = pb
            LogUI.event("playback ticket=\(pb.ticketId) source=\(pb.source?.id ?? "?") renditions=\(pb.renditions.count) audios=\(pb.audios.count)")
            rawSkips = pb.segments
            rebuildSkips()
            await open(pb)
            fetchSkips(pb)
        } catch {
            guard !stopped else { return }
            if let e = error as? ApiError {
                phase = .failed(e.offline ? "Нет связи с сервером" : "Не получилось открыть поток (\(e.code.isEmpty ? String(e.status) : e.code))")
            } else {
                phase = .failed("Не получилось открыть поток")
            }
        }
    }

    private func open(_ pb: Playback) async {
        guard let url = Api.absolute(pb.url) else {
            phase = .failed("Плохой адрес потока")
            return
        }
        let asset = AVURLAsset(url: url)
        let item = AVPlayerItem(asset: asset)
        if qualityMax > 0 {
            // «До Np»: потолок разрешения варианта HLS на новом элементе.
            item.preferredMaximumResolution = CGSize(width: qualityMax * 16 / 9, height: qualityMax)
        }
        await attach(item, asset: asset)
    }

    /// Новый элемент в плеер: наблюдатели, позиция, дорожки, старт.
    private func attach(_ item: AVPlayerItem, asset: AVAsset) async {
        guard !stopped else { return }
        observe(item)
        player.replaceCurrentItem(with: item)
        do {
            let d = try await asset.load(.duration).seconds
            guard !stopped, player.currentItem === item else { return }
            if d.isFinite, d > 0 {
                duration = d
                rebuildSkips()
            }
            if let seek = pendingSeek, seek > 1, d <= 0 || !d.isFinite || seek < d - 5 {
                await player.seek(to: CMTime(seconds: seek, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
                position = seek
            }
        } catch {
            // Длину не отдали — поток всё равно пробуем играть; ошибку
            // самого элемента поймает наблюдатель статуса.
        }
        guard !stopped, player.currentItem === item else { return }
        pendingSeek = nil
        if let urlAsset = asset as? AVURLAsset { await applyTrackSelection(urlAsset) }
        guard !stopped, player.currentItem === item else { return }
        if wantsPlay {
            player.playImmediately(atRate: speed)
        } else {
            player.pause()
        }
        phase = wantsPlay ? (player.timeControlStatus == .playing ? .playing : .buffering) : .paused
        autoplay = true
    }

    /// Выбор озвучки/субтитров в стриме: текущий выбор важнее запомненного.
    /// Сравнение — по NAME из плейлиста (trackName), не по displayName.
    /// У скачанного — только дорожки, что легли в пакет (assetCache): чужую
    /// без сети не сыграть. Повторы kinopub схлопнуты (TrackPlan).
    private func applyTrackSelection(_ asset: AVURLAsset) async {
        let cache = localEntry != nil ? asset.assetCache : nil
        do {
            if let audible = try await asset.loadMediaSelectionGroup(for: .audible) {
                let inFile = cache?.mediaSelectionOptions(in: audible)
                let wanted = choice.variantLabel ?? (audioLabel.isEmpty ? nil : audioLabel)
                let fallback = await audible.defaultOption?.trackName()
                // В пакете озвучек не нашлось — пусть система играет свою.
                let named = inFile?.isEmpty == true ? []
                    : await TrackPlan.options(in: audible, among: inFile, preferred: [wanted, fallback])
                if cache != nil {
                    // Меню — в порядке плейлиста, а не «выбранная первой».
                    localAudio = named.isEmpty ? [] : await TrackPlan.options(in: audible, among: inFile).map(\.name)
                    LogUI.event("local tracks audio=\(localAudio) first=\(named.first?.name ?? "-")")
                }
                if let (option, name) = named.first {
                    player.currentItem?.select(option, in: audible)
                    // Подпись — студия из общего списка, если она в имени дорожки.
                    audioLabel = voiceOptions.first { !$0.title.isEmpty && name.localizedCaseInsensitiveContains($0.title) }?.title
                        ?? (name.isEmpty ? "Дорожка 1" : name)
                }
            }
            if let legible = try await asset.loadMediaSelectionGroup(for: .legible) {
                let named = await TrackPlan.options(in: legible, among: cache?.mediaSelectionOptions(in: legible))
                subOptions = named.map(\.name).filter { !$0.isEmpty }
                if subLabel != "Выкл", let hit = named.first(where: { $0.1.localizedCaseInsensitiveContains(subLabel) }) {
                    player.currentItem?.select(hit.0, in: legible)
                    subLabel = hit.1
                } else {
                    player.currentItem?.select(nil, in: legible)
                    subLabel = "Выкл"
                }
            }
        } catch {
            // Без групп — играем как есть.
        }
    }

    // MARK: - Скипы

    /// Отрезки под фактическую длину потока (Android rebuildSkips).
    private func rebuildSkips() {
        let d = duration > 0 ? duration : Double(detail?.episodes.first(where: { $0.id == episodeId })?.durationSec ?? 0)
        skips = Skips.pick(rawSkips, duration: d)
    }

    /// Разметка, пришедшая отдельно от тикета (/episodes/{id}/skips).
    private func fetchSkips(_ pb: Playback) {
        Task {
            guard let extra = try? await Api.shared.skips(episodeId, source: pb.source?.id, variant: pb.variant),
                  !extra.isEmpty, !stopped, playback?.ticketId == pb.ticketId else { return }
            rawSkips = pb.segments + extra
            rebuildSkips()
        }
    }

    // MARK: - Наблюдение

    private func startObservers() {
        let interval = CMTime(seconds: 1, preferredTimescale: 10)
        timeObserver = player.addPeriodicTimeObserver(forInterval: interval, queue: .main) { [weak self] time in
            MainActor.assumeIsolated { self?.tick(time.seconds) }
        }
        player.publisher(for: \.timeControlStatus)
            .receive(on: DispatchQueue.main)
            .sink { [weak self] status in
                MainActor.assumeIsolated { self?.onTimeControl(status) }
            }
            .store(in: &cancellables)
        // Уход в фон и прерывания: сохранить место.
        let nc = NotificationCenter.default
        appObservers.append(nc.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.reportNow() }
        })
    }

    /// Наблюдатели конкретного элемента: конец, срыв, статус.
    private func observe(_ item: AVPlayerItem) {
        let nc = NotificationCenter.default
        itemObservers.forEach { nc.removeObserver($0) }
        itemObservers = []
        itemCancellables = []
        itemObservers.append(nc.addObserver(forName: .AVPlayerItemDidPlayToEndTime, object: item, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.onEnded() }
        })
        itemObservers.append(nc.addObserver(forName: .AVPlayerItemFailedToPlayToEndTime, object: item, queue: .main) { [weak self, weak item] _ in
            MainActor.assumeIsolated { self?.onItemFailed(item) }
        })
        item.publisher(for: \.status)
            .receive(on: DispatchQueue.main)
            .sink { [weak self, weak item] status in
                MainActor.assumeIsolated {
                    if status == .failed { self?.onItemFailed(item) }
                }
            }
            .store(in: &itemCancellables)
    }

    /// Фаза следует за плеером: буферизация кончается сама, пауза системы
    /// (звонок, другой звук) видна как пауза.
    private func onTimeControl(_ status: AVPlayer.TimeControlStatus) {
        switch phase {
        case .preparing, .ended, .failed: return
        default: break
        }
        switch status {
        case .playing:
            phase = .playing
        case .waitingToPlayAtSpecifiedRate:
            phase = .buffering
        case .paused:
            if phase == .playing || phase == .buffering {
                wantsPlay = false
                phase = .paused
                reportNow()
            }
        @unknown default:
            break
        }
    }

    private func tick(_ seconds: Double) {
        guard !stopped, phase != .preparing, seconds.isFinite else { return }
        position = seconds
        if let item = player.currentItem {
            let around = item.loadedTimeRanges.map(\.timeRangeValue).first {
                $0.start.seconds <= seconds + 0.5 && seconds <= $0.end.seconds
            }
            buffered = around?.end.seconds ?? seconds
        }
        checkSkipWindow()
        reportProgress()
    }

    private func checkSkipWindow() {
        guard duration > 0 else { return }
        if let active = Skips.active(skips, at: position), settings.skipMode == "auto", Skips.autoSeekable(active) {
            let key = "\(active.kind)@\(Int(active.start))"
            if !autoSkipped.contains(key) {
                autoSkipped.insert(key)
                player.seek(to: CMTime(seconds: active.end, preferredTimescale: 600))
                autoSkippedSegment = active
                autoSkippedAt = Date()
            }
        }
        if let at = autoSkippedAt, Date().timeIntervalSince(at) > 5 {
            autoSkippedSegment = nil
            autoSkippedAt = nil
        }
    }

    /// «Вернуть» после автопропуска: назад к началу отрезка, без повторного прыжка.
    func undoAutoSkip(_ seg: SkipSegment) {
        autoSkippedSegment = nil
        autoSkippedAt = nil
        seek(seg.start)
    }

    // MARK: - Прогресс

    /// Можно ли сейчас доверять позиции: поток открыт и на своём месте.
    private var positionSettled: Bool {
        pendingSeek == nil && phase != .preparing && position > 0
    }

    private func reportProgress() {
        guard positionSettled else { return }
        reportCount += 1
        guard reportCount % 10 == 0 else { return }
        send(final: false)
    }

    /// Пауза, фон, выход — сразу.
    func reportNow() {
        guard positionSettled else { return }
        send(final: false)
    }

    private func send(final: Bool) {
        let ms = Int64(position * 1000)
        guard ms > 0, final || ms != reportedPosition else { return }
        reportedPosition = ms
        if let entry = localEntry {
            let durMs = Int64(duration * 1000)
            // Досмотрено — с 92%, как PlayerActivity.kt.
            let watched = duration > 0 && position >= duration * 0.92
            LocalLibrary.shared.setProgress(entry.id, positionMs: ms, durationMs: durMs, watched: watched)
            return
        }
        let dur = Int64((duration > 0 ? duration : Double(detail?.episodes.first(where: { $0.id == episodeId })?.durationSec ?? 0)) * 1000)
        let iso = ISO8601DateFormatter().string(from: Date())
        let id = episodeId
        Task {
            try? await Api.shared.reportProgress(id, ProgressReport(positionMs: ms, durationMs: dur, clientTs: iso))
        }
    }

    // MARK: - Конец и сбои

    private func onEnded() {
        guard !stopped else { return }
        if duration > 0 { position = duration }
        send(final: true)
        if settings.autonext, let next = nextEpisode, localEntry == nil {
            let chain = (app?.autoChain ?? 0) + 1
            app?.autoChain = chain
            // Автоследующая: после трёх подряд — спросить.
            if chain >= 3 {
                confirmNext = true
                phase = .ended
            } else {
                app?.play(episodeId: next.id, titleId: titleId, autoplay: true, keepChain: true)
            }
        } else {
            phase = .ended
        }
    }

    /// Поток оборвался: 404/410 — ссылка протухла, свежую с той же площадки;
    /// иначе — другая площадка. Не больше трёх подъёмов за две минуты.
    private func onItemFailed(_ item: AVPlayerItem?) {
        guard !stopped, let item, player.currentItem === item else { return }
        let status = item.errorLog()?.events.last?.errorStatusCode ?? 0
        LogUI.event("player item failed http=\(status) err=\(item.error?.localizedDescription ?? "-")")
        if localEntry != nil {
            phase = .failed("Файл не открывается")
            return
        }
        let now = Date()
        recoverAttempts.removeAll { now.timeIntervalSince($0) > 120 }
        guard recoverAttempts.count < 3, let pb = playback else {
            phase = .failed("Поток обрывается: площадки не отдают серию")
            return
        }
        recoverAttempts.append(now)
        let gone = (status == 404 || status == 410) && !refreshedLink
        if gone {
            refreshedLink = true
            choice = PlaybackChoice(source: pb.source?.id, strict: true, variant: pb.variant,
                                    variantLabel: choice.variantLabel, voice: choice.voice, avoid: choice.avoid)
        } else if let cur = pb.source?.id {
            var avoid = choice.avoid
            if !avoid.contains(cur) { avoid.append(cur) }
            choice = PlaybackChoice(source: nil, strict: false, variant: nil,
                                    variantLabel: choice.variantLabel, voice: choice.voice, avoid: avoid)
        }
        issueTask?.cancel()
        issueTask = Task { await issue() }
    }

    // MARK: - Управление

    func togglePlay() {
        switch phase {
        case .playing, .buffering:
            wantsPlay = false
            player.pause()
            phase = .paused
            reportNow()
        case .paused:
            wantsPlay = true
            player.playImmediately(atRate: speed)
            phase = player.timeControlStatus == .playing ? .playing : .buffering
        case .ended:
            // Смотреть заново.
            wantsPlay = true
            confirmNext = false
            player.seek(to: .zero)
            player.playImmediately(atRate: speed)
            phase = .playing
        case .failed:
            issueTask?.cancel()
            issueTask = Task { await issue() }
        case .preparing:
            break
        }
        touchInput()
    }

    func seek(_ to: Double) {
        let clamped = min(max(0, to), duration > 0 ? duration : to)
        player.seek(to: CMTime(seconds: clamped, preferredTimescale: 600))
        position = clamped
        touchInput()
    }

    func skip(_ sec: Double) {
        seek(position + sec)
    }

    func setSpeed(_ s: Float) {
        speed = s
        if wantsPlay { player.rate = s }
        menu = nil
    }

    func setQuality(_ height: Int) {
        qualityMax = height
        menu = nil
        issueTask?.cancel()
        issueTask = Task { await issue() }
    }

    /// Выбор озвучки: ключ студии или подпись дорожки стрима.
    func setAudio(source: String? = nil, variant: String? = nil, variantLabel: String? = nil, voice: String? = nil, strict: Bool = false) {
        choice = PlaybackChoice(
            source: source ?? choice.source, strict: strict || choice.strict,
            variant: variant ?? choice.variant, variantLabel: variantLabel ?? choice.variantLabel,
            voice: voice ?? choice.voice, avoid: choice.avoid
        )
        if let variantLabel { audioLabel = variantLabel }
        menu = nil
        refreshedLink = false
        issueTask?.cancel()
        issueTask = Task { await issue() }
        if let label = variantLabel ?? voice {
            Task { _ = try? await Api.shared.setPrefs(titleId, PrefsPatch(audio: label, subtitle: nil, source: nil)) }
        }
    }

    /// Озвучка скачанного: дорожка из пакета, без нового тикета и без сети.
    func setLocalAudio(_ name: String) {
        audioLabel = name
        menu = nil
        Self.setLocalPref("audio", titleId, name)
        Task {
            guard let item = player.currentItem, let asset = item.asset as? AVURLAsset,
                  let audible = try? await asset.loadMediaSelectionGroup(for: .audible) else { return }
            let named = await TrackPlan.options(in: audible, among: asset.assetCache?.mediaSelectionOptions(in: audible))
            if let hit = named.first(where: { $0.name == name }) { item.select(hit.option, in: audible) }
            let now = item.currentMediaSelection.selectedMediaOption(in: audible)
            LogUI.event("local audio set want=\(name) now=\(await now?.trackName() ?? "-")")
        }
    }

    /// Выбор дорожек скачанного по тайтлу — на устройстве: следующая
    /// скачанная серия начнёт с той же озвучки и тех же субтитров без сети.
    private static func localPrefKey(_ kind: String, _ titleId: Int64) -> String {
        "local_\(kind)_\(ServerConfig.profile ?? "default")_\(titleId)"
    }

    static func localPref(_ kind: String, _ titleId: Int64) -> String? {
        guard titleId > 0 else { return nil }
        return UserDefaults.standard.string(forKey: localPrefKey(kind, titleId))
    }

    static func setLocalPref(_ kind: String, _ titleId: Int64, _ value: String) {
        guard titleId > 0 else { return }
        UserDefaults.standard.set(value, forKey: localPrefKey(kind, titleId))
    }

    func setSubtitle(_ label: String) {
        subLabel = label
        menu = nil
        let local = localEntry != nil
        if local { Self.setLocalPref("sub", titleId, label) }
        Task {
            // Субтитры — группы legible в стриме (у скачанного — из пакета).
            if let asset = player.currentItem?.asset as? AVURLAsset,
               let legible = try? await asset.loadMediaSelectionGroup(for: .legible) {
                if label == "Выкл" {
                    player.currentItem?.select(nil, in: legible)
                } else {
                    let inFile = local ? asset.assetCache?.mediaSelectionOptions(in: legible) : nil
                    let named = await TrackPlan.options(in: legible, among: inFile)
                    if let hit = named.first(where: { $0.name == label }) {
                        player.currentItem?.select(hit.option, in: legible)
                    }
                }
            }
            // Скачанное смотрят и без сети — выбор там живёт на устройстве.
            guard !local else { return }
            _ = try? await Api.shared.setPrefs(titleId, PrefsPatch(audio: nil, subtitle: label == "Выкл" ? "" : label, source: nil))
        }
    }

    /// Соседняя серия по кнопке человека — счётчик автосмен с нуля.
    func play(_ ep: Episode) {
        app?.play(episodeId: ep.id, titleId: titleId, autoplay: true)
    }

    /// «Остаться» после конца серии.
    func stay() {
        confirmNext = false
        app?.autoChain = 0
        phase = .paused
        wantsPlay = false
    }

    func touchInput() {
        lastInput = Date()
    }

    func stop() {
        guard !stopped else { return }
        reportNow()
        stopped = true
        startTask?.cancel()
        issueTask?.cancel()
        if let timeObserver { player.removeTimeObserver(timeObserver) }
        timeObserver = nil
        let nc = NotificationCenter.default
        (itemObservers + appObservers).forEach { nc.removeObserver($0) }
        itemObservers = []
        appObservers = []
        cancellables = []
        itemCancellables = []
        player.pause()
        player.replaceCurrentItem(with: nil)
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }

    private func configureAudioSession() {
        do {
            try AVAudioSession.sharedInstance().setCategory(.playback, mode: .moviePlayback)
            try AVAudioSession.sharedInstance().setActive(true)
        } catch {}
    }

    // MARK: - Статистика

    var statsLines: [String] {
        var lines: [String] = []
        if let item = player.currentItem {
            let wh = item.presentationSize
            if wh.width > 0 { lines.append("\(Int(wh.width))×\(Int(wh.height))") }
            if let ev = item.accessLog()?.events.last, ev.indicatedBitrate > 0 {
                lines.append(String(format: "%.1f Мбит/с", ev.indicatedBitrate / 1_000_000))
            }
        }
        if let source = playback?.source {
            lines.append(source.name)
        }
        if buffered > position {
            lines.append(String(format: "буфер %.0f с", buffered - position))
        }
        return lines
    }

    /// Время конца по текущей скорости.
    var endTime: String {
        guard duration > 0 else { return "" }
        let remaining = (duration - position) / Double(max(speed, 0.1))
        let date = Date().addingTimeInterval(remaining)
        let f = DateFormatter()
        f.dateFormat = "HH:mm"
        return f.string(from: date)
    }
}

extension AVMediaSelectionOption {
    /// NAME дорожки из HLS-плейлиста («01. Многоголосый. LostFilm (RUS)»).
    /// displayName на iOS — только язык («Russian») у всех дорожек сразу,
    /// по нему озвучку не выбрать.
    func trackName() async -> String {
        let titles = AVMetadataItem.metadataItems(from: commonMetadata, filteredByIdentifier: .commonIdentifierTitle)
        if let t = try? await titles.first?.load(.stringValue), !t.isEmpty { return t }
        return displayName
    }
}
