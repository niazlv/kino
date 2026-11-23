import Foundation

/// Бесконечный список — порт ui/common/Paging.kt.
///
/// Два протокола сервера под одной обёрткой Page<T>:
/// offset-based (continue, search, lists, new-episodes) и page-based
/// (browse, history). `more()` догружает при прокрутке, `refresh()` молча
/// перечитывает до 10 страниц, заменяя показанное, не сбрасывая скролл.
@MainActor
final class Paged<T: Codable & Identifiable>: ObservableObject {
    @Published private(set) var items: [T] = []
    @Published private(set) var state: State = .idle
    /// Всего на сервере (0 — сервер не сказал).
    @Published private(set) var total = 0

    enum State: Equatable {
        case idle, loading, done, failed(String)
    }

    private(set) var hasMore = false
    private var next: Int?          // следующий offset или page; nil — исчерпан
    private var shown = 0
    private var inFlight = false
    private let pageBased: Bool
    private let fetch: (Int) async throws -> Page<T>

    /// - Parameters:
    ///   - pageBased: true — курсор `page` с 1; false — курсор `offset` с 0.
    ///   - fetch: запрос одной страницы по номеру курсора.
    init(pageBased: Bool, fetch: @escaping (Int) async throws -> Page<T>) {
        self.pageBased = pageBased
        self.fetch = fetch
    }

    /// Первая загрузка: спиннер/ошибка видны только когда пусто.
    func initial() async {
        guard items.isEmpty, !inFlight else { return }
        inFlight = true
        state = .loading
        do {
            let first = pageBased ? 1 : 0
            let page = try await fetch(first)
            items = page.items
            shown = page.items.count
            apply(page, cursor: first)
            state = .done
        } catch {
            state = .failed((error as? ApiError)?.userMessage ?? "Не получилось загрузить")
        }
        inFlight = false
    }

    /// Догрузка следующего окна.
    func more() async {
        guard hasMore, let n = next, !inFlight else { return }
        inFlight = true
        do {
            let page = try await fetch(n)
            // Дедуп по id: сервер может отдать пересекающиеся окна.
            let seen = Set(items.map(\.idHash))
            items += page.items.filter { !seen.contains($0.idHash) }
            shown = items.count
            apply(page, cursor: n)
        } catch {
            // Тихо: догрузка не показывает ошибку, попробуют ещё раз.
        }
        inFlight = false
    }

    /// Молчаливое обновление: перечитывает столько, сколько было показано
    /// (не больше 10 страниц), и заменяет список. При ошибке оставляет старый.
    func refresh() async {
        guard !inFlight else { return }
        // Ещё ничего не показано — это первая загрузка, не обновление.
        if items.isEmpty, state != .done { await initial(); return }
        inFlight = true
        let wanted = max(shown, 1)
        do {
            var acc: [T] = []
            var cursor = pageBased ? 1 : 0
            var more = true
            var pages = 0
            var lastTotal = 0
            while more, pages < 10, pages == 0 || acc.count < wanted {
                let page = try await fetch(cursor)
                lastTotal = page.total
                let seen = Set(acc.map(\.idHash))
                acc += page.items.filter { !seen.contains($0.idHash) }
                pages += 1
                more = pageBased
                    ? (!page.items.isEmpty && (page.hasMore || acc.count < page.total))
                    : page.hasMore
                if pageBased { cursor += 1 } else { cursor = page.nextOffset }
            }
            items = acc
            shown = acc.count
            total = lastTotal
            hasMore = more
            next = more ? cursor : nil
            if state != .done { state = .done }
        } catch {
            // Молча.
        }
        inFlight = false
    }

    /// Убрать показанное сразу, не дожидаясь сервера (как cont.drop на Android).
    func drop(where match: (T) -> Bool) {
        items.removeAll(where: match)
        shown = items.count
    }

    /// Курсор страниц считаем сами: browse/history отвечают только
    /// {items,total} — без номера страницы (как Paging.kt на Android).
    private func apply(_ page: Page<T>, cursor: Int) {
        total = page.total
        hasMore = pageBased
            ? (!page.items.isEmpty && (page.hasMore || shown < page.total))
            : page.hasMore
        next = hasMore ? (pageBased ? cursor + 1 : page.nextOffset) : nil
    }
}

extension Identifiable {
    /// id как хеш для дедупликации (id бывают Int64 и String).
    var idHash: Int {
        if let i = id as? Int64 { return Int(i) }
        return String(describing: id).hashValue
    }
}
