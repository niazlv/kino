import SwiftUI

/// Страница тайтла — 1:1 с TitleScreen.kt: размытый постер фоном, большая
/// кнопка, отметки, озвучки, сюжет, сезоны и серии с кадрами.
struct TitleScreen: View {
    let app: AppState
    let titleId: Int64

    @Environment(\.dismiss) private var dismiss
    @State private var detail: TitleDetail?
    /// nil — отметки ещё не пришли: нажатие отправило бы весь объект с
    /// false и сняло бы «Слежу»/«Буду смотреть» (Android ждёт загрузки).
    @State private var marks: Marks?
    @State private var loadError: String?
    @State private var voiceNames: [String] = []
    @State private var season: Int = -1
    @State private var compact = false
    @State private var plotExpanded = false
    @State private var voicesExpanded = false
    @State private var downloadTarget: DownloadTarget?
    @State private var scrolled = false

    struct DownloadTarget: Identifiable {
        let episode: Episode?
        let seasonNo: Int
        var scope: DownloadDialog.Scope? = nil
        var id: String { "\(episode?.id ?? 0)-\(seasonNo)-\(String(describing: scope))" }
    }

    var body: some View {
        ZStack(alignment: .top) {
            Pal.bg.ignoresSafeArea()
            if let detail {
                content(detail)
            } else if let loadError {
                ErrorView(text: loadError) {
                    self.loadError = nil
                    Task { await reload() }
                }
                .padding(.top, 80)
            } else {
                SpinnerView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            headerOverlay
        }
        .navigationBarHidden(true)
        .task { await reload() }
        .onChange(of: app.player) { _, newValue in
            if newValue == nil { Task { await reload() } }
        }
        .sheet(item: $downloadTarget) { target in
            if let detail {
                DownloadDialog(app: app, detail: detail, episode: target.episode, seasonNo: target.seasonNo, initialScope: target.scope)
            }
        }
    }

    // MARK: Контент

    private func content(_ d: TitleDetail) -> some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 20) {
                TitleHeaderView(d: d, voiceNames: voiceNames)
                    .padding(.top, 44)
                watchButton(d)
                actionsRow(d)
                if !voiceNames.isEmpty {
                    voicesBlock
                }
                plotBlock(d)
                if !isMovie(d) {
                    seasonBar(d)
                    episodes(d)
                }
            }
            .padding(.bottom, 24)
            // Смещение содержимого (а не самой прокрутки) — когда подложить
            // фон под кнопку «назад».
            .background(
                GeometryReader { geo in
                    Color.clear.preference(
                        key: TitleScrollKey.self,
                        value: geo.frame(in: .named("titleScroll")).minY
                    )
                }
            )
        }
        .coordinateSpace(name: "titleScroll")
        .ignoresSafeArea(edges: .top)
        .onPreferenceChange(TitleScrollKey.self) { y in
            let now = y < -140
            if now != scrolled {
                withAnimation(.easeInOut(duration: 0.2)) { scrolled = now }
            }
        }
    }

    private var headerOverlay: some View {
        // Полоса высотой с кнопку: фон уходит под статус-бар, но не на весь экран.
        ZStack(alignment: .leading) {
            if scrolled {
                Pal.bg.ignoresSafeArea(edges: .top)
                    .transition(.opacity)
            }
            HStack {
                Button {
                    dismiss()
                } label: {
                    Image(systemName: "chevron.left")
                        .font(.system(size: 18, weight: .semibold))
                        .foregroundStyle(.white)
                        .frame(width: 44, height: 44)
                        .background(Color.black.opacity(0.4), in: Circle())
                }
                .buttonStyle(.plain)
                if scrolled, let d = detail {
                    Text(splitTitle(d.title.title).0)
                        .font(.kinoTitleSmall)
                        .foregroundStyle(Pal.textMain)
                        .lineLimit(1)
                        .transition(.opacity)
                }
                Spacer()
            }
            .padding(.horizontal, 12)
            .padding(.top, 4)
        }
        .frame(height: 52)
        .frame(maxHeight: .infinity, alignment: .top)
        .animation(.easeInOut(duration: 0.2), value: scrolled)
    }

    // MARK: Шапка

    private struct TitleHeaderView: View {
        let d: TitleDetail
        let voiceNames: [String]

        var body: some View {
            VStack(alignment: .leading, spacing: 0) {
                // Фон: размытый постер + градиент к фону.
                ZStack(alignment: .bottom) {
                    PosterImage(titleId: d.title.id, hasArt: d.title.hasArt, name: d.title.title)
                        .blur(radius: 36)
                        .opacity(0.5)
                        .clipped()
                    LinearGradient(colors: [.clear, Pal.bg], startPoint: .center, endPoint: .bottom)
                }
                .frame(height: 240)
                .frame(maxWidth: .infinity)
                .clipped()
                VStack(alignment: .leading, spacing: 10) {
                    HStack(alignment: .top, spacing: 14) {
                        PosterImage(titleId: d.title.id, hasArt: d.title.hasArt, name: d.title.title)
                            .aspectRatio(2 / 3, contentMode: .fit)
                            .frame(width: 122)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                            .shadow(color: .black.opacity(0.5), radius: 18, y: 8)
                            .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color.white.opacity(0.15)))
                        VStack(alignment: .leading, spacing: 6) {
                            let (rus, orig) = splitTitle(d.title.title)
                            Text(rus)
                                .font(.system(size: 22, weight: .bold))
                                .foregroundStyle(Pal.textMain)
                                .lineLimit(4)
                            if !orig.isEmpty {
                                Text(orig).font(.kinoBodyMedium).foregroundStyle(Pal.textDim)
                            }
                            Text(metaLine(d)).font(.kinoBodySmall).foregroundStyle(Color(hex: 0xD5DAE2))
                            Text(sizeLine(d)).font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                        }
                    }
                    chips(d)
                }
                .padding(.horizontal, 20)
                .padding(.top, -60)
            }
        }

        private func metaLine(_ d: TitleDetail) -> String {
            var parts: [String] = []
            let what = speciesWord(d.title.species, d.title.kind)
            if !what.isEmpty { parts.append(what) }
            if d.title.year > 0 { parts.append(String(d.title.year)) }
            parts.append(d.title.finished ? "завершён" : "выходит")
            return parts.joined(separator: " · ")
        }

        private func sizeLine(_ d: TitleDetail) -> String {
            if isMovie(d) {
                if let dur = d.episodes.first, dur.durationSec > 0 {
                    return fmtMins(dur.durationSec / 60)
                }
                return ""
            }
            let seasons = Set(d.episodes.map(\.seasonNo)).count
            let eps = d.episodes.count
            return "\(seasons) \(plural(seasons, "сезон", "сезона", "сезонов")) · \(eps) \(plural(eps, "серия", "серии", "серий"))"
        }

        private func chips(_ d: TitleDetail) -> some View {
            let items = chipsItems(d)
            return Group {
                if !items.isEmpty {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 6) {
                            ForEach(items, id: \.self) { chip in
                                Text(chip)
                                    .font(.kinoBodySmall)
                                    .foregroundStyle(Pal.textFaint)
                                    .padding(.horizontal, 10)
                                    .padding(.vertical, 5)
                                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.white.opacity(0.3)))
                            }
                        }
                    }
                }
            }
        }

        private func chipsItems(_ d: TitleDetail) -> [String] {
            var items: [String] = []
            if voiceNames.count > 1 {
                items.append("\(voiceNames.count) \(plural(voiceNames.count, "озвучка", "озвучки", "озвучек"))")
            }
            if !d.title.sources.isEmpty {
                items.append("\(d.title.sources.count) \(plural(d.title.sources.count, "источник", "источника", "источников"))")
            }
            items.append(contentsOf: d.title.genres.prefix(3))
            return items
        }
    }

    // MARK: Кнопка просмотра

    @ViewBuilder
    private func watchButton(_ d: TitleDetail) -> some View {
        if let up = nextUp(d) {
            VStack(spacing: 6) {
                PrimaryButton(title: up.label, icon: "play.fill") {
                    playEpisode(up.episode)
                }
                if !up.sub.isEmpty {
                    Text(up.sub).font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                }
            }
            .padding(.horizontal, 20)
        }
    }

    // MARK: Действия

    private func actionsRow(_ d: TitleDetail) -> some View {
        let marks = self.marks ?? Marks()
        return HStack(spacing: 0) {
            IconAction(
                icon: marks.watchlist ? "bookmark.fill" : "bookmark",
                label: "Буду смотреть", active: marks.watchlist
            ) { toggleMark(\.watchlist) }
            if !isMovie(d) {
                IconAction(
                    icon: marks.following ? "bell.fill" : "bell",
                    label: "Следить", active: marks.following
                ) { toggleMark(\.following) }
            }
            IconAction(icon: "arrow.down.circle", label: "Скачать") {
                let up = nextUp(d)
                downloadTarget = DownloadTarget(episode: up?.episode, seasonNo: up?.episode.seasonNo ?? firstSeason(d))
            }
            IconAction(
                icon: marks.hidden ? "eye" : "eye.slash",
                label: marks.hidden ? "Вернуть" : "Скрыть", active: marks.hidden
            ) { toggleMark(\.hidden) }
        }
        .padding(.horizontal, 20)
        .opacity(self.marks == nil ? 0.5 : 1)
        .disabled(self.marks == nil)
    }

    private func toggleMark(_ key: WritableKeyPath<Marks, Bool>) {
        guard let before = marks else { return }
        var updated = before
        updated[keyPath: key].toggle()
        marks = updated
        Task {
            do {
                marks = try await Api.shared.setMarks(titleId, updated)
            } catch {
                // Сервер не принял — вернуть как было, а не врать галочкой.
                marks = before
            }
        }
    }

    /// Скачанная серия играет с диска (как TitleScreen.kt), иначе — поток.
    /// Автозапуск всегда: «Продолжить» не должен открываться на паузе.
    private func playEpisode(_ ep: Episode) {
        let localId = LocalLibrary.entryId(titleId: titleId, season: ep.seasonNo, episode: ep.episodeNo)
        if LocalLibrary.shared.entry(localId) != nil {
            app.player = .local(entryId: localId)
        } else {
            app.play(episodeId: ep.id, titleId: titleId, autoplay: true)
        }
    }

    // MARK: Озвучки

    private var voicesBlock: some View {
        Button {
            withAnimation { voicesExpanded.toggle() }
        } label: {
            Panel {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Text("Озвучки").font(.kinoTitleSmall).foregroundStyle(Pal.textMain)
                        Text(String(voiceNames.count)).font(.kinoBodyMedium).foregroundStyle(Pal.textFaint)
                        Spacer()
                        Image(systemName: "chevron.down")
                            .font(.system(size: 14))
                            .foregroundStyle(Pal.textFaint)
                            .rotationEffect(.degrees(voicesExpanded ? 180 : 0))
                    }
                    if voicesExpanded {
                        VStack(alignment: .leading, spacing: 6) {
                            ForEach(voiceNames, id: \.self) { name in
                                Text(name).font(.kinoBody).foregroundStyle(Pal.textDim)
                            }
                        }
                    } else {
                        Text(voiceNames.prefix(4).joined(separator: " · "))
                            .font(.kinoBodyMedium)
                            .foregroundStyle(Pal.textDim)
                            .lineLimit(1)
                    }
                }
            }
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 20)
    }

    // MARK: Сюжет

    @ViewBuilder
    private func plotBlock(_ d: TitleDetail) -> some View {
        if let plot = d.title.plot, !plot.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                Text(plot)
                    .font(.kinoBodyMedium)
                    .foregroundStyle(Pal.textMain)
                    .lineLimit(plotExpanded ? nil : 3)
                if plot.count > 140 {
                    Button(plotExpanded ? "Свернуть" : "Читать дальше") {
                        withAnimation { plotExpanded.toggle() }
                    }
                    .font(.kinoBodyMedium)
                    .foregroundStyle(Pal.accent)
                    .buttonStyle(.plain)
                }
                if !d.title.sources.isEmpty {
                    Text("Источники: \(d.title.sources.map(\.name).joined(separator: ", "))")
                        .font(.kinoBodySmall)
                        .foregroundStyle(Pal.textFaint)
                }
            }
            .padding(.horizontal, 20)
        }
    }

    // MARK: Сезоны

    private func seasonBar(_ d: TitleDetail) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Серии").font(.kinoTitle).foregroundStyle(Pal.textMain)
                Spacer()
                Button {
                    withAnimation { compact.toggle() }
                } label: {
                    Image(systemName: compact ? "list.bullet" : "rectangle.stack")
                        .font(.system(size: 18))
                        .foregroundStyle(Pal.textDim)
                        .frame(width: 40, height: 40)
                }
                .buttonStyle(.plain)
                Menu {
                    if seasonsOf(d).count > 1 {
                        Button("Скачать сезон") {
                            downloadTarget = DownloadTarget(episode: nil, seasonNo: season, scope: .season(season))
                        }
                    }
                    Button("Скачать все серии") {
                        downloadTarget = DownloadTarget(episode: nil, seasonNo: season, scope: .all)
                    }
                } label: {
                    Image(systemName: "ellipsis")
                        .font(.system(size: 18))
                        .foregroundStyle(Pal.textDim)
                        .frame(width: 40, height: 40)
                }
            }
            seasonPills(d)
            seasonStatus(d)
        }
        .padding(.horizontal, 20)
    }

    @ViewBuilder
    private func seasonPills(_ d: TitleDetail) -> some View {
        let seasons = seasonsOf(d)
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(seasons, id: \.self) { s in
                    let watched = watchedSeasons(d).contains(s)
                    Pill(
                        title: s == 0 ? "Доп." : "Сезон \(s)",
                        icon: watched ? "checkmark" : nil,
                        selected: season == s
                    ) {
                        withAnimation { season = s }
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func seasonStatus(_ d: TitleDetail) -> some View {
        let eps = episodesOf(d, season)
        let done = eps.filter { $0.status == "completed" }.count
        let whole = watchedSeasons(d).contains(season)
        HStack {
            if whole {
                Label("Сезон \(season == 0 ? "" : "\(season) ")просмотрен", systemImage: "checkmark.circle.fill")
                    .font(.kinoBodySmall)
                    .foregroundStyle(Pal.accent)
            } else if !eps.isEmpty {
                Text("просмотрено \(done) из \(eps.count)").font(.kinoBodySmall).foregroundStyle(Pal.textFaint)
            }
            Spacer()
            if !eps.isEmpty {
                Button(whole ? "Снять отметки" : "Отметить сезон") {
                    Task { await markSeason(d, season, watched: !whole) }
                }
                .font(.kinoLabel)
                .foregroundStyle(Pal.accent)
                .buttonStyle(.plain)
            }
        }
    }

    private func markSeason(_ d: TitleDetail, _ season: Int, watched: Bool) async {
        withAnimation {
            detail = withSeasonWatched(d, season, watched: watched)
        }
        _ = try? await Api.shared.setSeasonWatched(titleId, season: season, watched: watched)
        await reload()
    }

    // MARK: Серии

    @ViewBuilder
    private func episodes(_ d: TitleDetail) -> some View {
        let eps = episodesOf(d, season)
        VStack(alignment: .leading, spacing: 12) {
            let up = nextUp(d)
            ForEach(Array(eps.enumerated()), id: \.element.id) { _, ep in
                EpisodeRowView(
                    ep: ep,
                    titleId: titleId,
                    titleHasArt: d.title.hasArt,
                    isCurrent: up?.episode.id == ep.id,
                    compact: compact,
                    downloaded: LocalLibrary.shared.entry(LocalLibrary.entryId(titleId: titleId, season: ep.seasonNo, episode: ep.episodeNo)) != nil
                ) { watched in
                    Task { await setEpisodeWatched(d, ep, watched) }
                } onPlay: {
                    playEpisode(ep)
                } onDownload: {
                    downloadTarget = DownloadTarget(episode: ep, seasonNo: ep.seasonNo)
                }
            }
        }
        .padding(.horizontal, 20)
    }

    private func setEpisodeWatched(_ d: TitleDetail, _ ep: Episode, _ watched: Bool) async {
        _ = try? await Api.shared.setWatched(ep.id, watched)
        await reload()
    }

    // MARK: Данные

    private func reload() async {
        do {
            async let marksReq = try? Api.shared.marks(titleId)
            let d = try await Api.shared.title(titleId)
            if season < 0 { season = nextUp(d)?.episode.seasonNo ?? firstSeason(d) }
            detail = d
            loadError = nil
            if let m = await marksReq { marks = m }
            if let upId = nextUp(d)?.episode.id,
               let list = try? await Api.shared.voices(upId) {
                voiceNames = list.voices.map(\.title).filter { !$0.isBlank }.removingDuplicates()
            }
            LogUI.event("title loaded \(d.title.title) eps=\(d.episodes.count) voices=\(voiceNames.count)")
        } catch {
            // Уже показанное оставляем; пусто — ошибка с «Повторить».
            if detail == nil {
                let e = error as? ApiError
                loadError = e?.offline == true ? "Нет связи с сервером" : "Тайтл не загрузился"
            }
        }
    }

    private func firstSeason(_ d: TitleDetail) -> Int {
        d.episodes.map(\.seasonNo).min() ?? 0
    }
}

extension String {
    var isBlank: Bool { trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
}

// MARK: - Ряд серии

private struct EpisodeRowView: View {
    let ep: Episode
    let titleId: Int64
    let titleHasArt: Bool
    let isCurrent: Bool
    let compact: Bool
    var downloaded = false
    var onWatched: (Bool) -> Void
    var onPlay: () -> Void
    var onDownload: () -> Void

    var body: some View {
        Group {
            if compact {
                compactRow
            } else {
                fullRow
            }
        }
        // Долгое нажатие — те же действия, что в «⋯» (в компактном ряду их больше нигде нет).
        .cardMenu([
            CardAction(title: watched ? "Не просмотрено" : "Просмотрено", icon: watched ? "eye.slash" : "checkmark") { onWatched(!watched) },
            CardAction(title: "Скачать серию", icon: "arrow.down.circle") { onDownload() },
        ])
    }

    private var watched: Bool { ep.status == "completed" }
    private var started: Bool { ep.positionMs > 0 && !watched }

    private var fullRow: some View {
        Button(action: onPlay) {
            HStack(alignment: .top, spacing: 12) {
                ShotImage(episodeId: ep.id, fallbackTitleId: titleId, fallbackHasArt: titleHasArt, name: ep.title ?? "")
                    .aspectRatio(16 / 9, contentMode: .fit)
                    .frame(width: 140)
                    .clipShape(RoundedRectangle(cornerRadius: 10))
                    .overlay {
                        if watched {
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
                        if started {
                            ProgressLine(frac: ep.frac).background(Color.black.opacity(0.4))
                        }
                    }
                VStack(alignment: .leading, spacing: 4) {
                    Text("N серия".replacingOccurrences(of: "N", with: String(ep.episodeNo)))
                        .font(.kinoTitleSmall)
                        .foregroundStyle(Pal.textMain)
                    if let title = ep.title, !title.isBlank {
                        Text(title).font(.kinoBodySmall).foregroundStyle(Pal.textDim).lineLimit(2)
                    }
                    Text(infoLine)
                        .font(.kinoBodySmall)
                        .foregroundStyle(started ? Pal.accent : Pal.textFaint)
                }
                Spacer()
                Menu {
                    Button(watched ? "Не просмотрено" : "Просмотрено") { onWatched(!watched) }
                    Button("Скачать серию") { onDownload() }
                } label: {
                    Image(systemName: "ellipsis")
                        .font(.system(size: 16))
                        .foregroundStyle(Pal.textFaint)
                        .frame(width: 32, height: 32)
                }
            }
            .padding(8)
            .background(
                RoundedRectangle(cornerRadius: 10)
                    .fill(isCurrent ? Pal.accent.opacity(0.06) : .clear)
            )
        }
        .buttonStyle(.plain)
    }

    private var infoLine: String {
        let base = infoBase
        return downloaded ? (base.isEmpty ? "скачано" : "\(base) · скачано") : base
    }

    private var infoBase: String {
        if watched { return "просмотрено" }
        if started {
            let total = ep.durationSec * 1000
            return "осталось \(fmtMins((total - ep.positionMs) / 60_000))"
        }
        if ep.durationSec > 0 { return fmtMins(ep.durationSec / 60) }
        return ""
    }

    private var compactRow: some View {
        Button(action: onPlay) {
            HStack(spacing: 10) {
                ZStack {
                    Circle()
                        .fill(started || watched ? Pal.accent.opacity(0.2) : Pal.surface2)
                        .frame(width: 36, height: 36)
                    if watched {
                        Image(systemName: "checkmark").font(.system(size: 13, weight: .bold)).foregroundStyle(Pal.accent)
                    } else {
                        Text(String(ep.episodeNo))
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(started ? Pal.accent : Pal.textDim)
                    }
                }
                VStack(alignment: .leading, spacing: 2) {
                    if let title = ep.title, !title.isBlank {
                        Text(title).font(.kinoBodySmall).foregroundStyle(Pal.textMain).lineLimit(1)
                    } else {
                        Text("N серия".replacingOccurrences(of: "N", with: String(ep.episodeNo)))
                            .font(.kinoBodySmall).foregroundStyle(Pal.textMain)
                    }
                    if started {
                        ProgressLine(frac: ep.frac).frame(width: 90)
                    }
                }
                Spacer()
            }
        }
        .buttonStyle(.plain)
    }
}

extension Episode {
    var frac: Double {
        guard durationSec > 0 else { return 0 }
        return Double(positionMs) / Double(durationSec * 1000)
    }
}

private struct TitleScrollKey: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) {
        value = nextValue()
    }
}
