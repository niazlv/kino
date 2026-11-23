import SwiftUI

/// Корень. Смена сервера пересобирает всё дерево с новым AppState (аналог
/// перезапуска процесса на Android): ни один экран прежнего сервера не
/// доживает до нового — ни его состояние, ни его задачи (.task гаснут вместе
/// с видом).
struct RootView: View {
    @State private var generation = 0

    var body: some View {
        SessionRoot()
            .id(generation)
            .onReceive(NotificationCenter.default.publisher(for: .serverChanged)) { _ in
                generation += 1
            }
            #if DEBUG
            // Прогон смены сервера без нажатий: KINO_AUTO_SERVER=<адрес> через
            // 3 с после запуска переключает так же, как экран «Сервер».
            .task {
                guard let raw = ProcessInfo.processInfo.environment["KINO_AUTO_SERVER"],
                      let url = ServerAddress.normalize(raw) else { return }
                try? await Task.sleep(nanoseconds: 3_000_000_000)
                await ServerSwitch.apply(url, info: try? await ServerProbe.fetch(url))
            }
            #endif
    }
}

/// Машина состояний AppRoot — validating → needsServer | login | offline | ready.
struct SessionRoot: View {
    @State private var app = AppState()

    var body: some View {
        Group {
            switch app.sessionState {
            case .validating:
                ZStack {
                    Pal.bg.ignoresSafeArea()
                    SpinnerView(size: 36)
                }
            case .needsServer:
                // Сборка без вшитого сервера: сначала адрес, потом вход.
                ServerScreen(setup: true)
            case .login:
                LoginScreen(app: app)
            case .offline:
                OfflineRoot(app: app)
            case .ready:
                NavRoot(app: app)
            }
        }
        .preferredColorScheme(.dark)
        .task { await app.validateSession() }
        .onReceive(NotificationCenter.default.publisher(for: .sessionExpired)) { _ in
            // Мёртвую пару — из Keychain, иначе следующий запуск снова с ней.
            Api.shared.clearSession()
            app.me = nil
            app.player = nil
            app.paths = [:]
            app.sessionState = .login
        }
        .onReceive(NotificationCenter.default.publisher(for: .serverWillChange)) { _ in
            // Плеер закрывается (на закрытии он шлёт позицию прежнему серверу),
            // экраны гаснут до спиннера — до пересборки корня ничего не грузится.
            app.player = nil
            app.paths = [:]
            app.sessionState = .validating
        }
        .fullScreenCover(item: $app.player) { req in
            PlayerHost(app: app, request: req)
        }
    }
}

extension AppState.PlayerRequest: Identifiable {
    var id: String {
        switch self {
        case let .stream(episodeId, titleId, autoplay): return "s\(episodeId)-\(titleId)-\(autoplay)"
        case let .local(entryId): return "l\(entryId)"
        }
    }
}

/// Офлайн-витрина: баннер + загрузки (пока каркас, файлы — фаза 4).
struct OfflineRoot: View {
    let app: AppState

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: "wifi.slash").font(.system(size: 14))
                Text("Нет связи с сервером").font(.kinoBodyMedium)
                Spacer()
                Button("Повторить") { Task { await app.validateSession() } }
                    .font(.kinoLabel)
                    .foregroundStyle(Pal.accent)
                    .buttonStyle(.plain)
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 10)
            .background(Pal.surface2)
            DownloadsScreen(app: app, offline: true)
        }
        .background(Pal.bg.ignoresSafeArea())
    }
}

/// Навигация: 5 вкладок, у каждой свой стек — как NavRoot с bottomBar.
struct NavRoot: View {
    let app: AppState

    var body: some View {
        TabView(selection: Binding(
            get: { app.selectedTab },
            set: { app.selectedTab = $0 }
        )) {
            tab(.home, "Главная", "house") { HomeScreen(app: app) }
            tab(.catalog, "Каталог", "square.grid.2x2") { CatalogScreen(app: app) }
            tab(.search, "Поиск", "magnifyingglass") { SearchScreen(app: app) }
            tab(.mine, "Моё", "bookmark") { MineScreen(app: app) }
            tab(.downloads, "Загрузки", "arrow.down.circle") { DownloadsScreen(app: app) }
        }
        .tint(Pal.accent)
        .toolbarBackground(Pal.surface1, for: .tabBar)
        .scrollContentBackground(.hidden)
    }

    @ViewBuilder
    private func tab(_ tab: AppState.Tab, _ title: String, _ icon: String, @ViewBuilder content: @escaping () -> some View) -> some View {
        NavigationStack(path: Binding(
            get: { app.path(tab) },
            set: { app.paths[tab] = $0 }
        )) {
            content()
                .navigationDestination(for: AppState.Route.self) { route in
                    routeView(route)
                }
        }
        .tabItem {
            Label(title, systemImage: icon)
        }
        .tag(tab)
    }

    @ViewBuilder
    private func routeView(_ route: AppState.Route) -> some View {
        switch route {
        case let .title(id):
            TitleScreen(app: app, titleId: id)
        case .settings:
            SettingsScreen(app: app)
        case .history:
            HistoryScreen(app: app)
        case .link:
            LinkDeviceScreen(app: app)
        }
    }
}
