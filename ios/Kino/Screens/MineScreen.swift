import SwiftUI

/// «Моё» — 4 таба; 1:1 с ListsScreen.kt.
struct MineScreen: View {
    let app: AppState

    static let tabs = ["Слежу", "Новые серии", "Буду смотреть", "История"]

    var body: some View {
        VStack(spacing: 12) {
            ScreenHeader(
                title: "Моё",
                actions: {
                    AnyView(
                        Button { app.push(.mine, .settings) } label: {
                            Avatar(name: app.me?.displayName ?? " ")
                        }
                        .buttonStyle(.plain)
                    )
                }
            )
            .padding(.horizontal, 20)
            .padding(.top, 8)
            PillRow(items: (0..<Self.tabs.count).map { i in
                PillRow.PillItem(id: String(i), title: Self.tabs[i], selected: app.mineTab == i) {
                    app.mineTab = i
                }
            })
            tabBody
                .frame(maxHeight: .infinity)
        }
        .background(Pal.bg.ignoresSafeArea())
        .navigationBarHidden(true)
    }

    @ViewBuilder
    private var tabBody: some View {
        switch app.mineTab {
        // Как ListsScreen.kt: «Слежу» — все подписки, «Новые серии» — только с новыми.
        case 0: FollowGrid(app: app, onlyNew: false)
        case 1: FollowGrid(app: app, onlyNew: true)
        case 2: WatchlistGrid(app: app)
        default: HistoryList(app: app)
        }
    }
}

/// Сетка «слежу/новые серии»: постеры с бейджем «+N».
struct FollowGrid: View {
    let app: AppState
    let onlyNew: Bool

    @StateObject private var paged: Paged<NewEpisodeItem>

    init(app: AppState, onlyNew: Bool) {
        self.app = app
        self.onlyNew = onlyNew
        _paged = StateObject(wrappedValue: Paged<NewEpisodeItem>(pageBased: false) {
            try await Api.shared.newEpisodes(offset: $0, onlyNew: onlyNew)
        })
    }

    var body: some View {
        PagedScroll(paged: paged) {
            EmptyState(
                icon: onlyNew ? "sparkles" : "bell",
                title: onlyNew ? "Новых серий нет" : "Пока ни за чем не следите",
                text: onlyNew ? "Всё, за чем вы следите, досмотрено." : "Нажмите «Следить» на странице сериала — новые серии появятся здесь."
            )
        } content: {
            LazyVGrid(columns: posterColumns, spacing: 16) {
                ForEach(Array(paged.items.enumerated()), id: \.element.id) { idx, item in
                    PosterCard(
                        titleId: item.titleId, hasArt: item.hasArt, name: item.title,
                        sub: item.unwatched > 0 ? "\(item.unwatched) \(plural(item.unwatched, "новая", "новые", "новых"))" : "всё просмотрено",
                        badge: item.unwatched > 0 ? "+\(item.unwatched)" : nil,
                        dim: item.unwatched == 0 && !onlyNew
                    ) { app.openTitle(item.titleId) }
                    .onAppear {
                        if idx >= paged.items.count - 6 { Task { await paged.more() } }
                    }
                }
            }
        }
    }
}

/// Сетка «Буду смотреть».
struct WatchlistGrid: View {
    let app: AppState

    @StateObject private var paged: Paged<Title> = .init(pageBased: false) {
        try await Api.shared.list("watchlist", offset: $0)
    }

    var body: some View {
        PagedScroll(paged: paged) {
            EmptyState(icon: "bookmark", title: "Список пуст", text: "Отмечайте «Буду смотреть» на странице тайтла, чтобы не потерять.")
        } content: {
            LazyVGrid(columns: posterColumns, spacing: 16) {
                ForEach(Array(paged.items.enumerated()), id: \.element.id) { idx, title in
                    TitleCard(title: title) { app.openTitle(title.id) }
                        .onAppear {
                            if idx >= paged.items.count - 6 { Task { await paged.more() } }
                        }
                }
            }
        }
    }
}

/// Колонки постерной сетки (Adaptive 104, как posterCells() на Android).
let posterColumns = [GridItem(.adaptive(minimum: 104), spacing: 12, alignment: .top)]

/// Прокрутка над Paged: скелет/ошибка/пусто во всю ширину, содержимое,
/// догрузка. Первый показ — initial, возврат на экран — тихий refresh
/// (после «Не следить» или отметки список не залёживается).
struct PagedScroll<T: Codable & Identifiable, Empty: View, Content: View>: View {
    @ObservedObject var paged: Paged<T>
    var skeleton: AnyView = AnyView(PosterGridSkeleton())
    @ViewBuilder var empty: () -> Empty
    @ViewBuilder var content: () -> Content

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                switch paged.state {
                case .idle:
                    skeleton
                case .loading where paged.items.isEmpty:
                    skeleton
                case .failed where paged.items.isEmpty:
                    ErrorView(text: paged.errorMessage) { Task { await paged.initial() } }
                case .done where paged.items.isEmpty:
                    empty()
                default:
                    content()
                    if paged.hasMore { LoadMore() }
                }
            }
            .padding(.horizontal, 20)
            .padding(.top, 4)
            .padding(.bottom, 24)
        }
        .task {
            if paged.items.isEmpty { await paged.initial() } else { await paged.refresh() }
        }
    }
}

/// «История» — отдельный экран из профиля.
struct HistoryScreen: View {
    let app: AppState

    var body: some View {
        VStack(spacing: 12) {
            ScreenHeader(title: "История").padding(.horizontal, 20).padding(.top, 8)
            HistoryList(app: app)
        }
        .background(Pal.bg.ignoresSafeArea())
        .navigationBarHidden(true)
    }
}

/// Лента истории, сгруппированная по дням; 1:1 с ListsScreen.HistoryList.
struct HistoryList: View {
    let app: AppState

    @StateObject private var paged: Paged<HistoryItem> = .init(pageBased: true) {
        try await Api.shared.history(page: $0)
    }

    private static let dayFmt: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "ru_RU")
        f.dateFormat = "d MMMM"
        return f
    }()

    var body: some View {
        PagedScroll(paged: paged, skeleton: AnyView(HStack { Spacer(); SpinnerView(); Spacer() })) {
            EmptyState(icon: "clock.arrow.circlepath", title: "Истории пока нет", text: "Здесь появится то, что вы смотрите.")
        } content: {
            let groups = Dictionary(grouping: paged.items) { dayKey($0.updatedAt) }
            let order = paged.items.map { dayKey($0.updatedAt) }.removingDuplicates()
            LazyVStack(alignment: .leading, spacing: 14) {
                ForEach(order, id: \.self) { day in
                    Text(day).font(.kinoTitleSmall).foregroundStyle(Pal.textDim)
                    ForEach(groups[day] ?? []) { item in
                        HistoryRow(item: item) { app.openTitle(item.titleId) }
                            .onAppear {
                                if item.id == paged.items.last?.id { Task { await paged.more() } }
                            }
                    }
                }
            }
        }
    }

    private func dayKey(_ iso: String) -> String {
        let df = ISO8601DateFormatter()
        df.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        var date = df.date(from: iso)
        if date == nil {
            let alt = ISO8601DateFormatter()
            date = alt.date(from: iso)
        }
        guard let date else { return "" }
        let cal = Calendar.current
        if cal.isDateInToday(date) { return "Сегодня" }
        if cal.isDateInYesterday(date) { return "Вчера" }
        return Self.dayFmt.string(from: date)
    }
}

struct HistoryRow: View {
    let item: HistoryItem
    var onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            HStack(alignment: .top, spacing: 12) {
                VStack(spacing: 4) {
                    ShotImage(episodeId: item.episodeId, fallbackTitleId: item.titleId, fallbackHasArt: item.hasArt, name: item.title)
                        .aspectRatio(16 / 9, contentMode: .fit)
                        .frame(width: 132)
                        .clipShape(RoundedRectangle(cornerRadius: 10))
                        .overlay {
                            if item.status == "completed" {
                                Color.black.opacity(0.55)
                                Image(systemName: "checkmark").font(.system(size: 20)).foregroundStyle(Pal.onAccent)
                            } else if item.positionMs > 0 {
                                Image(systemName: "play.fill").font(.system(size: 16)).foregroundStyle(.white)
                            }
                        }
                    ProgressLine(frac: item.frac)
                        .frame(width: 132)
                }
                VStack(alignment: .leading, spacing: 3) {
                    Text(item.title).font(.kinoTitleSmall).foregroundStyle(Pal.textMain).lineLimit(2)
                    Text(entrySub)
                        .font(.kinoBodySmall)
                        .foregroundStyle(Pal.textDim)
                }
                Spacer()
            }
        }
        .buttonStyle(.plain)
    }

    private var entrySub: String {
        var parts: [String] = []
        if item.seasonNo > 0 { parts.append("\(item.seasonNo) сезон, \(item.episodeNo) серия") }
        parts.append(item.status == "completed" ? "просмотрено" : "не досмотрено")
        return parts.joined(separator: " · ")
    }
}

extension Array where Element: Hashable {
    func removingDuplicates() -> [Element] {
        var seen = Set<Element>()
        return filter { seen.insert($0).inserted }
    }
}

extension HistoryItem {
    var frac: Double {
        guard durationMs > 0 else { return 0 }
        return Double(positionMs) / Double(durationMs)
    }
}
