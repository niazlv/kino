import SwiftUI

/// «Главная» — 1:1 с HomeScreen.kt: герой «Продолжить», рельсы, скелеты.
struct HomeScreen: View {
    let app: AppState

    @StateObject private var cont = Paged<ContinueItem>(pageBased: false) {
        try await Api.shared.continueItems(offset: $0)
    }
    @StateObject private var fresh = Paged<NewEpisodeItem>(pageBased: false) {
        try await Api.shared.newEpisodes(offset: $0, onlyNew: false)
    }
    @StateObject private var watchlist = Paged<Title>(pageBased: false) {
        try await Api.shared.list("watchlist", offset: $0)
    }
    @StateObject private var movies = Paged<Title>(pageBased: true) {
        try await Api.shared.browse(kind: "movie", genre: nil, sort: "new", page: $0)
    }
    @StateObject private var series = Paged<Title>(pageBased: true) {
        try await Api.shared.browse(kind: "series", genre: nil, sort: "new", page: $0)
    }
    @State private var refreshKey = 0

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 24) {
                topRow
                continueSection
                freshRail
                watchlistRail
                moviesRail
                seriesRail
            }
            .padding(.bottom, 24)
        }
        .refreshable { await refreshAll(force: true) }
        .background(Pal.bg.ignoresSafeArea())
        .navigationBarHidden(true)
        .task {
            guard cont.items.isEmpty else { return }
            await loadAll()
        }
        // Возврат из плеера — тихое обновление списков.
        .onChange(of: app.player) { _, newValue in
            if newValue == nil { Task { await refreshAll(force: false) } }
        }
    }

    // MARK: Шапка

    private var topRow: some View {
        HStack {
            BrandMark()
            Spacer()
            Button {
                app.push(.home, .settings)
            } label: {
                Avatar(name: app.me?.displayName ?? " ")
            }
            .buttonStyle(.plain)
        }
        .padding(.horizontal, 20)
        .padding(.top, 8)
    }

    // MARK: Продолжить

    @ViewBuilder
    private var continueSection: some View {
        switch cont.state {
        case .loading where cont.items.isEmpty:
            HeroSkeleton().padding(.horizontal, 20)
        case .failed where cont.items.isEmpty:
            ErrorView(text: cont.errorMessage) { Task { await loadAll() } }
        default:
            if let hero = cont.items.first {
                VStack(alignment: .leading, spacing: 12) {
                    continueHero(hero)
                    PrimaryButton(title: hero.playLabel, icon: "play.fill") {
                        app.play(episodeId: hero.episodeId, titleId: hero.titleId, autoplay: hero.positionMs <= 0)
                    }
                    .padding(.horizontal, 20)
                    if cont.items.count > 1 || cont.hasMore {
                        SectionHeader(title: "Продолжить смотреть", count: cont.totalCount)
                            .padding(.horizontal, 20)
                        Rail {
                            ForEach(Array(cont.items.dropFirst().enumerated()), id: \.element.id) { idx, item in
                                WideCard(item: item, actions: continueActions(item)) {
                                    app.play(episodeId: item.episodeId, titleId: item.titleId, autoplay: item.positionMs <= 0)
                                }
                                .onAppear {
                                    if idx >= cont.items.count - 6 { Task { await cont.more() } }
                                }
                            }
                            if cont.hasMore { LoadMore() }
                        }
                    }
                }
            } else {
                EmptyState(
                    icon: "sparkles.tv",
                    title: "Пока ничего не начато",
                    text: "Начните смотреть — здесь появится продолжение.",
                    actionTitle: "Открыть каталог"
                ) { app.selectedTab = .catalog }
            }
        }
    }

    private func continueHero(_ item: ContinueItem) -> some View {
        VStack(spacing: 0) {
            Button {
                app.play(episodeId: item.episodeId, titleId: item.titleId, autoplay: item.positionMs <= 0)
            } label: {
                ShotImage(episodeId: item.episodeId, fallbackTitleId: item.titleId, fallbackHasArt: item.hasArt, name: item.title)
                    .aspectRatio(16 / 10, contentMode: .fit)
                    .frame(maxWidth: .infinity)
                    .clipShape(RoundedRectangle(cornerRadius: 22))
                    .overlay(
                        LinearGradient(colors: [.clear, .black.opacity(0.85)], startPoint: .center, endPoint: .bottom)
                            .clipShape(RoundedRectangle(cornerRadius: 22))
                    )
                    .overlay(alignment: .bottomLeading) {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("ПРОДОЛЖИТЬ")
                                .font(.kinoOverline)
                                .tracking(1.2)
                                .foregroundStyle(Pal.accent)
                            Text(splitTitle(item.title).0)
                                .font(.kinoHeadline)
                                .foregroundStyle(.white)
                                .lineLimit(2)
                            let orig = splitTitle(item.title).1
                            if !orig.isEmpty {
                                Text(orig).font(.kinoBodySmall).foregroundStyle(.white.opacity(0.7))
                            }
                            Text(describeContinue(item))
                                .font(.kinoBodySmall)
                                .foregroundStyle(.white.opacity(0.75))
                            ProgressLine(frac: item.frac, height: 4).padding(.top, 4)
                        }
                        .padding(16)
                    }
            }
            .buttonStyle(.plain)
            .cardMenu(continueActions(item))
        }
        .padding(.horizontal, 20)
    }

    // MARK: Рельсы

    @ViewBuilder
    private var freshRail: some View {
        if !fresh.items.isEmpty || fresh.state == .loading {
            VStack(alignment: .leading, spacing: 10) {
                SectionHeader(title: "Новые серии", count: fresh.totalCount, actionTitle: "Все") {
                    app.openMine(tab: 1)
                }
                .padding(.horizontal, 20)
                Rail {
                    ForEach(Array(fresh.items.enumerated()), id: \.element.id) { idx, item in
                        PosterCard(
                            titleId: item.titleId, hasArt: item.hasArt, name: item.title,
                            sub: kindSubtitle(item.kind, item.year, nil),
                            badge: item.unwatched > 0 ? "+\(item.unwatched)" : nil,
                            width: 128
                        ) { app.openTitle(item.titleId) }
                        .onAppear {
                            if idx >= fresh.items.count - 4 { Task { await fresh.more() } }
                        }
                    }
                    if fresh.hasMore { LoadMore() }
                }
            }
        }
    }

    @ViewBuilder
    private var watchlistRail: some View {
        if !watchlist.items.isEmpty || watchlist.state == .loading {
            VStack(alignment: .leading, spacing: 10) {
                SectionHeader(title: "Буду смотреть", count: watchlist.totalCount, actionTitle: "Все") {
                    app.openMine(tab: 2)
                }
                .padding(.horizontal, 20)
                Rail {
                    ForEach(Array(watchlist.items.enumerated()), id: \.element.id) { idx, item in
                        PosterCard(titleId: item.id, hasArt: item.hasArt, name: item.title, sub: kindSubtitle(item.kind, item.year, item.species), width: 128) {
                            app.openTitle(item.id)
                        }
                        .onAppear {
                            if idx >= watchlist.items.count - 4 { Task { await watchlist.more() } }
                        }
                    }
                    if watchlist.hasMore { LoadMore() }
                }
            }
        }
    }

    @ViewBuilder
    private var moviesRail: some View {
        if !movies.items.isEmpty || movies.state == .loading {
            railSection("Новые фильмы", paged: movies) { item in
                PosterCard(titleId: item.id, hasArt: item.hasArt, name: item.title, sub: kindSubtitle(item.kind, item.year, item.species), width: 128) {
                    app.openTitle(item.id)
                }
            }
        }
    }

    @ViewBuilder
    private var seriesRail: some View {
        if !series.items.isEmpty || series.state == .loading {
            railSection("Новые сериалы", paged: series) { item in
                PosterCard(titleId: item.id, hasArt: item.hasArt, name: item.title, sub: kindSubtitle(item.kind, item.year, item.species), width: 128) {
                    app.openTitle(item.id)
                }
            }
        }
    }

    private func railSection(_ title: String, paged: Paged<Title>, @ViewBuilder card: @escaping (Title) -> some View) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            SectionHeader(title: title, actionTitle: "Все") {
                app.selectedTab = .catalog
            }
            .padding(.horizontal, 20)
            Rail {
                ForEach(Array(paged.items.enumerated()), id: \.element.id) { idx, item in
                    card(item)
                        .onAppear {
                            if idx >= paged.items.count - 4 { Task { await paged.more() } }
                        }
                }
                if paged.hasMore { LoadMore() }
            }
        }
    }

    // MARK: Загрузка

    private func loadAll() async {
        refreshKey += 1
        LogUI.event("home load begin")
        await withTaskGroup(of: Void.self) { group in
            group.addTask { await self.cont.initial() }
            group.addTask { await self.fresh.initial() }
            group.addTask { await self.watchlist.initial() }
            group.addTask { await self.movies.initial() }
            group.addTask { await self.series.initial() }
        }
        LogUI.event("home loaded cont=\(cont.items.count) fresh=\(fresh.items.count) watchlist=\(watchlist.items.count) movies=\(movies.items.count) series=\(series.items.count)")
    }

    /// Тихое обновление: перечитывает показанное, не сбрасывая скролл.
    private func refreshAll(force: Bool) async {
        await withTaskGroup(of: Void.self) { group in
            group.addTask { await self.cont.refresh() }
            group.addTask { await self.fresh.refresh() }
            group.addTask { await self.watchlist.refresh() }
            group.addTask { await self.movies.refresh() }
            group.addTask { await self.series.refresh() }
        }
        LogUI.event("home loaded cont=\(cont.items.count) fresh=\(fresh.items.count) watchlist=\(watchlist.items.count) movies=\(movies.items.count) series=\(series.items.count)")
    }

    /// Долгое нажатие на «Продолжить» — как лист на Android.
    private func continueActions(_ item: ContinueItem) -> [CardAction] {
        [
            CardAction(title: "Продолжить", icon: "play.fill") {
                app.play(episodeId: item.episodeId, titleId: item.titleId, autoplay: item.positionMs <= 0)
            },
            CardAction(title: "О тайтле", icon: "info.circle") { app.openTitle(item.titleId) },
            CardAction(title: "Убрать из «Продолжить»", icon: "eye.slash", destructive: true) {
                hideFromContinue(item)
            },
        ]
    }

    /// Скрыть тайтл: остальные отметки («Слежу», «Буду смотреть») не трогаем —
    /// читаем их и меняем только hidden, как Android.
    private func hideFromContinue(_ item: ContinueItem) {
        cont.drop { $0.titleId == item.titleId }
        Task {
            do {
                var m = try await Api.shared.marks(item.titleId)
                m.hidden = true
                _ = try await Api.shared.setMarks(item.titleId, m)
            } catch {
                await cont.refresh()
            }
        }
    }
}

func sheetRow(icon: String, title: String, action: @escaping () -> Void) -> some View {
    Button(action: action) {
        HStack(spacing: 12) {
            Image(systemName: icon).font(.system(size: 18)).frame(width: 24)
            Text(title).font(.kinoBody)
            Spacer()
        }
        .foregroundStyle(Pal.textMain)
        .padding(.vertical, 12)
    }
    .buttonStyle(.plain)
}

extension Paged {
    var errorMessage: String {
        if case let .failed(message) = state { return message }
        return ""
    }
    var totalCount: Int? { total > 0 ? total : nil }
}

extension ContinueItem {
    var playLabel: String {
        if seasonNo > 0 { return "Смотреть \(seasonNo)×\(episodeNo)" }
        return "Продолжить"
    }
}
