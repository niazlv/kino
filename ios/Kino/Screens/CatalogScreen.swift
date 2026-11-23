import SwiftUI

/// «Каталог» — 1:1 с CatalogScreen.kt: пилюли, жанры, сортировка, сетка.
struct CatalogScreen: View {
    let app: AppState

    static let sorts: [(id: String, title: String)] = [
        ("new", "Сначала новое"),
        ("popular", "Популярное"),
        ("year", "По году выхода"),
        ("title", "По названию"),
    ]
    static let kinds: [(id: String, title: String)] = [
        ("", "Всё"), ("movie", "Фильмы"), ("series", "Сериалы"),
    ]

    @State private var kind = ""
    @State private var genre: String?
    @State private var sort = "new"
    @State private var genres: [String] = []
    @State private var showGenrePicker = false
    @State private var paged: Paged<Title>?
    @State private var builtKey = ""

    private var filterKey: String { "\(kind)|\(genre ?? "")|\(sort)" }

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 16) {
                header
                if let paged {
                    // Отдельный вид с @ObservedObject: @State с классом внутри
                    // не видит его @Published — догрузка не перерисовывалась.
                    TitleGrid(paged: paged, emptyText: "Каталог наполняется обходом площадок…",
                              retry: { rebuild(force: true) }) { app.openTitle($0) }
                        .padding(.horizontal, 20)
                }
            }
            .padding(.bottom, 24)
        }
        .background(Pal.bg.ignoresSafeArea())
        .navigationBarHidden(true)
        // Возврат на вкладку не сбрасывает сетку и скролл: пересборка — только
        // при смене фильтра.
        .task(id: filterKey) { rebuild(force: false) }
        .task { await loadGenres() }
        .sheet(isPresented: $showGenrePicker) { genrePicker }
    }

    // MARK: Шапка

    private var header: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                Text("Каталог").font(.kinoHeadline).foregroundStyle(Pal.textMain)
                if let total = paged?.items.count, total > 0, paged?.hasMore == false {
                    Text("\(total) в разделе").font(.kinoBodyMedium).foregroundStyle(Pal.textDim)
                }
                Spacer()
                Menu {
                    ForEach(Self.sorts, id: \.id) { s in
                        Button {
                            sort = s.id
                        } label: {
                            if sort == s.id {
                                Label(s.title, systemImage: "checkmark")
                            } else {
                                Text(s.title)
                            }
                        }
                    }
                } label: {
                    Image(systemName: "arrow.up.arrow.down")
                        .font(.system(size: 18))
                        .foregroundStyle(Pal.textDim)
                        .frame(width: 44, height: 44)
                }
            }
            .padding(.horizontal, 20)
            PillRow(items: Self.kinds.map { k in
                PillRow.PillItem(id: k.id, title: k.title, selected: kind == k.id) { kind = k.id }
            })
            genreRow
        }
        .padding(.vertical, 8)
    }

    private var genreRow: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                Pill(title: "Все жанры", selected: genre == nil) { genre = nil }
                ForEach(topGenres, id: \.self) { g in
                    Pill(title: g, selected: genre == g) { genre = g }
                }
                if let genre, !topGenres.contains(genre) {
                    Pill(title: genre, selected: true) {}
                }
                Pill(title: "Ещё жанры…", icon: "chevron.down") { showGenrePicker = true }
            }
            .padding(.horizontal, 20)
        }
    }

    private var topGenres: [String] {
        Array(genres.prefix(16))
    }

    private var genrePicker: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Жанры").font(.kinoTitle).foregroundStyle(Pal.textMain)
            ScrollView {
                FlowLayout(spacing: 8) {
                    Pill(title: "Все жанры", selected: genre == nil) {
                        genre = nil
                        showGenrePicker = false
                    }
                    ForEach(sortedGenres, id: \.self) { g in
                        Pill(title: g, selected: genre == g) {
                            genre = g
                            showGenrePicker = false
                        }
                    }
                }
            }
        }
        .padding(20)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Pal.surface1)
        .preferredColorScheme(.dark)
    }

    /// Жанры: кириллица раньше латиницы, внутри — по алфавиту.
    private var sortedGenres: [String] {
        genres.sorted {
            let c1 = $0.isCyrillic, c2 = $1.isCyrillic
            if c1 != c2 { return c1 }
            return $0.localizedCompare($1) == .orderedAscending
        }
    }

    // MARK: Сетка

    private func rebuild(force: Bool) {
        guard force || builtKey != filterKey || paged == nil else { return }
        builtKey = filterKey
        let kind = kind, genre = genre, sort = sort
        let p = Paged<Title>(pageBased: true) {
            try await Api.shared.browse(kind: kind.isEmpty ? nil : kind, genre: genre, sort: sort, page: $0)
        }
        paged = p
        Task { await p.initial() }
    }

    private func loadGenres() async {
        guard genres.isEmpty else { return }
        let page = try? await Api.shared.genres()
        genres = page?.items ?? []
        LogUI.event("catalog genres=\(genres.count)")
    }
}

/// Сетка постеров над Paged: скелет, ошибка, пусто, догрузка.
struct TitleGrid: View {
    @ObservedObject var paged: Paged<Title>
    var emptyText = ""
    var retry: () -> Void
    var open: (Int64) -> Void

    var body: some View {
        switch paged.state {
        case .idle:
            PosterGridSkeleton()
        case .loading where paged.items.isEmpty:
            PosterGridSkeleton()
        case .failed where paged.items.isEmpty:
            ErrorView(text: paged.errorMessage, retry: retry)
        case .done where paged.items.isEmpty:
            EmptyState(icon: "magnifyingglass", title: "Здесь пока пусто", text: emptyText)
        default:
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 104), spacing: 12, alignment: .top)], spacing: 16) {
                ForEach(Array(paged.items.enumerated()), id: \.element.id) { idx, title in
                    TitleCard(title: title) { open(title.id) }
                        .onAppear {
                            if idx >= paged.items.count - 6 { Task { await paged.more() } }
                        }
                }
            }
            if paged.hasMore { LoadMore() }
        }
    }
}

extension String {
    var isCyrillic: Bool { contains(where: { ("а"..."я").contains($0.lowercased()) || $0 == "ё" }) }
}

/// Простой flow-layout для пилюль (аналог FlowRow).
struct FlowLayout: Layout {
    var spacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? 320
        var x: CGFloat = 0, y: CGFloat = 0, rowH: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x + size.width > width, x > 0 {
                x = 0
                y += rowH + spacing
                rowH = 0
            }
            x += size.width + spacing
            rowH = max(rowH, size.height)
        }
        return CGSize(width: width, height: y + rowH)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, rowH: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x + size.width > bounds.maxX, x > bounds.minX {
                x = bounds.minX
                y += rowH + spacing
                rowH = 0
            }
            view.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            rowH = max(rowH, size.height)
        }
    }
}
