import SwiftUI

/// Глобальное состояние приложения — аналог AppRoot + Nav.kt (Profile.me).
@MainActor
@Observable
final class AppState {
    /// needsServer — сборка без вшитого сервера, и человек ещё не выбрал свой.
    enum SessionState: Equatable { case validating, needsServer, login, offline, ready }
    enum Tab: String, CaseIterable, Hashable {
        case home, catalog, search, mine, downloads
    }
    enum Route: Hashable {
        case title(Int64)
        case settings
        case history
        case link
    }
    /// Запрос плеера: стриминг с сервера или локальный файл.
    enum PlayerRequest: Equatable {
        case stream(episodeId: Int64, titleId: Int64, autoplay: Bool)
        case local(entryId: String)
    }

    var sessionState: SessionState = .validating
    var me: Me?
    var selectedTab: Tab = {
        #if DEBUG
        if let t = ProcessInfo.processInfo.environment["KINO_AUTOTAB"], let tab = Tab(rawValue: t) {
            return tab
        }
        #endif
        return .home
    }()
    var mineTab = 0
    var paths: [Tab: [Route]] = [:]
    var player: PlayerRequest?

    /// Сервер для подписи «@login · сервер»: имя из визитки или хост.
    var host: String { ServerConfig.label }

    func path(_ tab: Tab) -> [Route] { paths[tab] ?? [] }

    func push(_ tab: Tab, _ route: Route) {
        paths[tab, default: []].append(route)
    }

    func openTitle(_ id: Int64) {
        push(selectedTab, .title(id))
    }

    /// Вкладка «Моё» с начальным табом (route mine?tab=N).
    func openMine(tab: Int) {
        mineTab = tab
        selectedTab = .mine
    }

    /// Сколько серий подряд сменил autonext без человека (после трёх — «Вы ещё смотрите?»).
    var autoChain = 0

    func play(episodeId: Int64, titleId: Int64, autoplay: Bool = true, keepChain: Bool = false) {
        if !keepChain { autoChain = 0 }
        player = .stream(episodeId: episodeId, titleId: titleId, autoplay: autoplay)
    }

    // MARK: Сессия

    /// Проверка сессии на старте: GET /me.
    func validateSession() async {
        if ServerConfig.needsServer {
            LogUI.event("no server -> setup")
            sessionState = .needsServer
            return
        }
        if Api.shared.token == nil {
            LogUI.event("session none -> login")
            sessionState = .login
            return
        }
        do {
            me = try await Api.shared.me()
            sessionState = .ready
            LogUI.event("session ok user=\(me?.login ?? "?")")
            #if DEBUG
            let env = ProcessInfo.processInfo.environment
            if let id = env["KINO_AUTO_TITLE"], let tid = Int64(id) {
                push(selectedTab, .title(tid))
            }
            if let id = env["KINO_AUTO_PLAY"] {
                player = .stream(episodeId: Int64(id) ?? 101, titleId: 1, autoplay: true)
            }
            if let id = env["KINO_AUTO_DOWNLOAD"], let eid = Int64(id) {
                let entryId = LocalLibrary.entryId(titleId: 1, season: 1, episode: 1)
                Task {
                    await DownloadManager.shared.enqueue(
                        entryId: entryId, title: "Тьма", episodeLabel: "1 сезон, 1 серия",
                        episodeId: eid, voice: nil
                    )
                }
            }
            if let id = env["KINO_AUTO_LOCAL"], let entry = LocalLibrary.shared.entry(id) {
                player = .local(entryId: entry.id)
            }
            #endif
        } catch let e as ApiError {
            if e.sessionDead {
                Api.shared.clearSession()
                me = nil
                sessionState = .login
            } else {
                sessionState = .offline
            }
        } catch {
            LogUI.event("session fail offline: \(error)")
            sessionState = .offline
        }
    }

    func onLoginDone() async {
        await validateSession()
    }

    func logout() async {
        await Auth.logout()
        me = nil
        paths = [:]
        selectedTab = .home
        sessionState = .login
    }
}
