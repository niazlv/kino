import SwiftUI

/// «Загрузки» — 1:1 с DownloadsScreen.kt: статус загрузки, хранилище,
/// секции «Можно смотреть»/«Просмотрено», синк прогресса при открытии.
struct DownloadsScreen: View {
    let app: AppState
    var offline = false

    @ObservedObject private var library = LocalLibrary.shared
    @ObservedObject private var manager = DownloadManager.shared
    @State private var syncedCount: Int?
    @State private var confirmWipe = false
    @State private var confirmRemove: LocalLibrary.Entry?

    var body: some View {
        VStack(spacing: 0) {
            ScreenHeader(title: "Загрузки", subtitle: subtitle)
                .padding(.horizontal, 20)
                .padding(.top, 8)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 16) {
                    // Все задачи: идущие, на паузе и сорванные (с «Повторить»).
                    ForEach(manager.jobs) { job in
                        statusCard(job)
                    }
                    if let err = manager.lastError, !manager.jobs.contains(where: { if case .failed = $0.state { return true } else { return false } }) {
                        Panel {
                            VStack(alignment: .leading, spacing: 4) {
                                Text("Последняя загрузка не удалась")
                                    .font(.kinoBody)
                                    .foregroundStyle(Pal.danger)
                                Text(err).font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                            }
                        }
                    }
                    storageCard
                    if library.entries.isEmpty {
                        EmptyState(
                            icon: "arrow.down.circle",
                            title: "Скачанного пока нет",
                            text: offline
                                ? "Скачанное появится здесь и будет доступно без сети."
                                : "Скачивайте серии из карточки тайтла — они будут доступны офлайн."
                        )
                    } else {
                        section("Можно смотреть", watched: false)
                        section("Просмотрено", watched: true)
                    }
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 24)
            }
        }
        .background(Pal.bg.ignoresSafeArea())
        .navigationBarHidden(true)
        .task {
            guard !offline else { return }
            let n = await Sync.pushLocalProgress()
            if n > 0 { syncedCount = n }
        }
        .alert("Стереть просмотренное?", isPresented: $confirmWipe) {
            Button("Стереть", role: .destructive) { library.wipeWatched() }
            Button("Отмена", role: .cancel) {}
        } message: {
            Text("Файлы удаляются насовсем. Освободится \(fmtBytes(library.entries.filter(\.watched).reduce(0) { $0 + $1.size })).")
        }
        .alert("Стереть \(confirmRemove.map { entryLabel($0) } ?? "")?", isPresented: Binding(
            get: { confirmRemove != nil },
            set: { if !$0 { confirmRemove = nil } }
        )) {
            Button("Стереть", role: .destructive) {
                if let e = confirmRemove { library.remove(e.id) }
                confirmRemove = nil
            }
            Button("Отмена", role: .cancel) { confirmRemove = nil }
        } message: {
            Text("Файл удаляется насовсем.")
        }
    }

    private var subtitle: String? {
        if offline { return "Офлайн" }
        if let n = syncedCount, n > 0 { return "Прогресс отправлен: \(n)" }
        return nil
    }

    // MARK: Карточки

    private func statusCard(_ job: DownloadManager.Job) -> some View {
        Panel {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 10) {
                    switch job.state {
                    case .running:
                        SpinnerView(size: 20)
                    case .failed:
                        Image(systemName: "exclamationmark.triangle").font(.system(size: 18)).foregroundStyle(Pal.danger)
                    default:
                        Image(systemName: "pause.circle").font(.system(size: 18)).foregroundStyle(Pal.textDim)
                    }
                    VStack(alignment: .leading, spacing: 1) {
                        Text(job.title).font(.kinoBody).foregroundStyle(Pal.textMain).lineLimit(1)
                        Text(jobLine(job))
                            .font(.kinoBodySmall).foregroundStyle(Pal.textDim).lineLimit(2)
                    }
                    Spacer()
                    Button {
                        switch job.state {
                        case .paused: manager.resume(job.id)
                        case .failed: manager.retry(job.id)
                        default: manager.pause(job.id)
                        }
                    } label: {
                        Image(systemName: jobActionIcon(job)).font(.system(size: 16)).foregroundStyle(Pal.textDim)
                            .frame(width: 36, height: 36)
                    }
                    .buttonStyle(.plain)
                    Button {
                        manager.cancel(job.id)
                    } label: {
                        Image(systemName: "xmark").font(.system(size: 16)).foregroundStyle(Pal.danger)
                            .frame(width: 36, height: 36)
                    }
                    .buttonStyle(.plain)
                }
                ProgressLine(frac: job.percent / 100, height: 6)
            }
        }
    }

    /// Во время догрузки дорожек видео уже в «Можно смотреть» — строка
    /// говорит, что именно догружается: «· озвучка 1 из 2 · 40%».
    private func jobLine(_ job: DownloadManager.Job) -> String {
        let label = job.stage.map { "\(job.episodeLabel) · \($0)" } ?? job.episodeLabel
        switch job.state {
        case .failed(let why): return "\(label) · не удалось: \(why)"
        case .paused: return "\(label) · на паузе · \(Int(job.percent))%"
        default: return "\(label) · \(Int(job.percent))%"
        }
    }

    private func jobActionIcon(_ job: DownloadManager.Job) -> String {
        switch job.state {
        case .paused: return "play.fill"
        case .failed: return "arrow.clockwise"
        default: return "pause"
        }
    }

    private var storageCard: some View {
        Panel {
            VStack(alignment: .leading, spacing: 8) {
                Text(fmtBytes(library.bytes)).font(.kinoTitle).foregroundStyle(Pal.textMain)
                HStack {
                    Text("\(library.entries.count) \(plural(library.entries.count, "файл", "файла", "файлов"))")
                        .font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                    Spacer()
                    if library.entries.contains(where: \.watched) {
                        Button("Стереть просмотренное") { confirmWipe = true }
                            .font(.kinoBodySmall)
                            .foregroundStyle(Pal.danger)
                            .buttonStyle(.plain)
                    }
                }
            }
        }
    }

    private func section(_ title: String, watched: Bool) -> some View {
        let items = library.entries
            .filter { $0.watched == watched }
            .sorted { ($0.title, $0.season, $0.episode) < ($1.title, $1.season, $1.episode) }
        return Group {
            if !items.isEmpty {
                VStack(alignment: .leading, spacing: 10) {
                    SectionHeader(title: title, count: items.count)
                    ForEach(items) { entry in
                        DownloadRow(entry: entry) {
                            app.player = .local(entryId: entry.id)
                        } onWatched: {
                            library.setWatched(entry.id, !entry.watched)
                        } onRemove: {
                            confirmRemove = entry
                        }
                    }
                }
            }
        }
    }

    private func entryLabel(_ e: LocalLibrary.Entry) -> String {
        e.movie ? "«\(e.title)»" : "\(e.title) — \(e.season) сезон, \(e.episode) серия"
    }
}

/// Ряд скачанного файла.
struct DownloadRow: View {
    let entry: LocalLibrary.Entry
    var onPlay: () -> Void
    var onWatched: () -> Void
    var onRemove: () -> Void

    var body: some View {
        Button(action: onPlay) {
            HStack(alignment: .top, spacing: 12) {
                ZStack {
                    ArtPlaceholder(name: entry.title)
                        .aspectRatio(16 / 9, contentMode: .fit)
                        .frame(width: 140)
                        .clipShape(RoundedRectangle(cornerRadius: 10))
                    if entry.watched {
                        Color.black.opacity(0.55)
                        Image(systemName: "checkmark").font(.system(size: 20)).foregroundStyle(Pal.onAccent)
                    } else {
                        ZStack {
                            Circle().fill(Color.black.opacity(0.5)).frame(width: 32, height: 32)
                            Image(systemName: "play.fill").font(.system(size: 13)).foregroundStyle(.white)
                        }
                    }
                }
                .overlay(alignment: .bottom) {
                    if entry.positionMs > 0, entry.durationMs > 0 {
                        ProgressLine(frac: Double(entry.positionMs) / Double(entry.durationMs))
                            .background(Color.black.opacity(0.4))
                    }
                }
                VStack(alignment: .leading, spacing: 3) {
                    Text(entry.title).font(.kinoTitleSmall).foregroundStyle(Pal.textMain).lineLimit(2)
                    Text("\(entry.movie ? "Фильм" : "\(entry.season) сезон, \(entry.episode) серия") · \(fmtBytes(entry.size))")
                        .font(.kinoBodySmall)
                        .foregroundStyle(Pal.textDim)
                }
                Spacer()
                Menu {
                    Button(entry.watched ? "Смотреть сначала" : "Смотреть") { onPlay() }
                    Button(entry.watched ? "Не просмотрено" : "Просмотрено") { onWatched() }
                    Button("Стереть", role: .destructive) { onRemove() }
                } label: {
                    Image(systemName: "ellipsis")
                        .font(.system(size: 16))
                        .foregroundStyle(Pal.textFaint)
                        .frame(width: 32, height: 32)
                }
            }
        }
        .buttonStyle(.plain)
    }
}
