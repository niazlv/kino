import SwiftUI

/// «Поиск» — 1:1 с SearchScreen.kt: саггесты, история, результаты.
struct SearchScreen: View {
    let app: AppState

    @State private var query = ""
    @State private var submitted: String?
    @State private var suggestions: [Title] = []
    @State private var history: [String] = []
    @State private var paged: Paged<Title>?

    var body: some View {
        VStack(spacing: 12) {
            searchField
                .padding(.horizontal, 20)
                .padding(.top, 8)
            bodyContent
        }
        .background(Pal.bg.ignoresSafeArea())
        .navigationBarHidden(true)
        .task { await loadHistory() }
        .task(id: query) { await loadSuggestions() }
        .onChange(of: submitted) { _, q in
            guard let q, !q.isEmpty else { return }
            let query = q
            let p = Paged<Title>(pageBased: false) {
                try await Api.shared.search(query, offset: $0)
            }
            paged = p
            Task { await p.initial() }
            Task {
                if let h = try? await Api.shared.addSearchHistory(q) { history = h }
            }
        }
    }

    /// Результаты — пока в поле тот же запрос; правка текста возвращает
    /// подсказки (как SearchScreen.kt: submitted == query.trim()).
    private var showingResults: Bool {
        guard let submitted else { return false }
        return query.trimmingCharacters(in: .whitespaces) == submitted
    }

    // MARK: Поле

    private var searchField: some View {
        HStack(spacing: 10) {
            Image(systemName: "magnifyingglass").font(.system(size: 16)).foregroundStyle(Pal.textDim)
            TextField("", text: $query)
                .font(.kinoBody)
                .foregroundStyle(Pal.textMain)
                .submitLabel(.search)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .onSubmit { submit() }
            if !query.isEmpty {
                Button {
                    query = ""
                    submitted = nil
                    suggestions = []
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 16))
                        .foregroundStyle(Pal.textFaint)
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 14)
        .frame(height: 48)
        .background(Pal.surface2, in: RoundedRectangle(cornerRadius: 16))
    }

    private func submit() {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return }
        submitted = q
        suggestions = []
    }

    // MARK: Тело

    @ViewBuilder
    private var bodyContent: some View {
        if showingResults, let q = submitted {
            results(q)
        } else if query.trimmingCharacters(in: .whitespaces).count >= 2, !suggestions.isEmpty {
            suggestionsList
        } else {
            historyBlock
        }
    }

    // MARK: Саггесты

    private var suggestionsList: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 0) {
                ForEach(suggestions) { title in
                    // Подсказка ведёт сразу на тайтл, как на Android.
                    Button {
                        app.openTitle(title.id)
                    } label: {
                        HStack(spacing: 12) {
                            PosterImage(titleId: title.id, hasArt: title.hasArt, name: title.title)
                                .frame(width: 44, height: 66)
                                .clipShape(RoundedRectangle(cornerRadius: 8))
                            VStack(alignment: .leading, spacing: 2) {
                                Text(title.title).font(.kinoBody).foregroundStyle(Pal.textMain).lineLimit(1)
                                Text(kindSubtitle(title.kind, title.year, title.species))
                                    .font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                            }
                            Spacer()
                            Image(systemName: "arrow.up.forward")
                                .font(.system(size: 14))
                                .foregroundStyle(Pal.textFaint)
                        }
                        .padding(.horizontal, 20)
                        .padding(.vertical, 8)
                    }
                    .buttonStyle(.plain)
                }
                Button {
                    submit()
                } label: {
                    HStack {
                        Spacer()
                        Text("Искать «\(query)» везде")
                            .font(.kinoLabel)
                            .foregroundStyle(Pal.accent)
                        Spacer()
                    }
                    .padding(.vertical, 14)
                }
                .buttonStyle(.plain)
            }
        }
    }

    private func loadSuggestions() async {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !showingResults, q.count >= 2 else {
            if q.count < 2 { suggestions = [] }
            return
        }
        // Дребезг 250 мс: .task(id:) отменит ожидание, если текст сменился.
        try? await Task.sleep(nanoseconds: 250_000_000)
        guard !Task.isCancelled else { return }
        let page = try? await Api.shared.suggest(q, limit: 8)
        guard !Task.isCancelled, q == query.trimmingCharacters(in: .whitespaces) else { return }
        suggestions = page?.items ?? []
    }

    // MARK: История

    private var historyBlock: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                if history.isEmpty {
                    EmptyState(
                        icon: "magnifyingglass",
                        title: "Что будем смотреть?",
                        text: "Ищите по названию на русском или в оригинале."
                    )
                } else {
                    HStack {
                        Text("Недавние").font(.kinoTitleMedium).foregroundStyle(Pal.textMain)
                        Spacer()
                        Button("Очистить") {
                            Task {
                                if let h = try? await Api.shared.clearSearchHistory() { history = h }
                            }
                        }
                        .font(.kinoBodyMedium)
                        .foregroundStyle(Pal.textDim)
                        .buttonStyle(.plain)
                    }
                    FlowLayout(spacing: 8) {
                        ForEach(history, id: \.self) { h in
                            Pill(
                                title: h, icon: "clock.arrow.circlepath",
                                trailing: {
                                    AnyView(
                                        Button {
                                            Task {
                                                if let left = try? await Api.shared.removeSearchHistory(h) { history = left }
                                            }
                                        } label: {
                                            Image(systemName: "xmark")
                                                .font(.system(size: 11, weight: .semibold))
                                                .foregroundStyle(Pal.textFaint)
                                        }
                                        .buttonStyle(.plain)
                                    )
                                },
                                selected: false
                            ) {
                                query = h
                                submit()
                            }
                        }
                    }
                }
            }
            .padding(.horizontal, 20)
        }
    }

    private func loadHistory() async {
        history = (try? await Api.shared.searchHistory()) ?? []
    }

    // MARK: Результаты

    private func results(_ q: String) -> some View {
        Group {
            if let paged {
                PagedScroll(paged: paged) {
                    EmptyState(icon: "magnifyingglass", title: "Ничего не нашлось", text: "Попробуйте другое название.")
                } content: {
                    SearchResultsGrid(paged: paged, app: app)
                }
                .id(q)
            }
        }
    }
}

/// Сетка результатов с «Найдено: N».
private struct SearchResultsGrid: View {
    @ObservedObject var paged: Paged<Title>
    let app: AppState

    var body: some View {
        Text("Найдено: \(paged.total > 0 ? paged.total : paged.items.count)\(paged.total == 0 && paged.hasMore ? "+" : "")")
            .font(.kinoBodyMedium)
            .foregroundStyle(Pal.textDim)
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
