import SwiftUI

/// «Профиль» — 1:1 с SettingsScreen.kt.
struct SettingsScreen: View {
    let app: AppState

    @Environment(\.dismiss) private var dismiss
    @State private var settings: TokenStore.Settings = TokenStore.settings
    @State private var showPasswordDialog = false
    @State private var showLogoutConfirm = false
    @State private var showServers = false

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                if !app.path(app.selectedTab).isEmpty {
                    Button { dismiss() } label: {
                        Image(systemName: "chevron.left")
                            .font(.system(size: 18, weight: .semibold))
                            .foregroundStyle(Pal.textMain)
                            .frame(width: 44, height: 44)
                            .background(Color.black.opacity(0.4), in: Circle())
                    }
                    .buttonStyle(.plain)
                }
                Text("Профиль").font(.kinoHeadline).foregroundStyle(Pal.textMain)
                Spacer()
            }
            .padding(.horizontal, 20)
            .padding(.top, 8)

            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    accountPanel
                    navGroup
                    playerGroup
                    codecsGroup
                    downloadsGroup
                    accountGroup
                }
                .padding(20)
            }
        }
        .background(Pal.bg.ignoresSafeArea())
        .navigationBarHidden(true)
        .sheet(isPresented: $showPasswordDialog) { passwordDialog }
        .sheet(isPresented: $showServers) { ServerScreen() }
        .alert("Выйти?", isPresented: $showLogoutConfirm) {
            Button("Выйти", role: .destructive) { Task { await app.logout() } }
            Button("Отмена", role: .cancel) {}
        } message: {
            Text("Прогресс сохранится на сервере. Скачанное останется на устройстве.")
        }
    }

    // MARK: Аккаунт

    private var accountPanel: some View {
        Panel {
            HStack(spacing: 14) {
                Avatar(name: app.me?.displayName ?? " ", size: 56)
                VStack(alignment: .leading, spacing: 2) {
                    Text(app.me?.displayName ?? "").font(.kinoTitle).foregroundStyle(Pal.textMain)
                    Text("@\(app.me?.login ?? "") · \(app.host)")
                        .font(.kinoBodySmall)
                        .foregroundStyle(Pal.textDim)
                }
                Spacer()
            }
        }
    }

    private var navGroup: some View {
        VStack(spacing: 0) {
            navRow(icon: "tv", title: "Подключить устройство", sub: "Вход на телевизоре по коду") {
                app.push(app.selectedTab, .link)
            }
            Divider().overlay(Pal.stroke).padding(.leading, 68)
            navRow(icon: "clock.arrow.circlepath", title: "История просмотров") {
                app.push(app.selectedTab, .history)
            }
        }
        .background(Pal.surface1, in: RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(Pal.stroke))
    }

    private func navRow(icon: String, title: String, sub: String = "", action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 12) {
                ZStack {
                    RoundedRectangle(cornerRadius: 9).fill(Pal.surface3).frame(width: 36, height: 36)
                    Image(systemName: icon).font(.system(size: 17)).foregroundStyle(Pal.textMain)
                }
                VStack(alignment: .leading, spacing: 1) {
                    Text(title).font(.kinoBody).foregroundStyle(Pal.textMain)
                    if !sub.isEmpty {
                        Text(sub).font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                    }
                }
                Spacer()
                Image(systemName: "chevron.right").font(.system(size: 13)).foregroundStyle(Pal.textFaint)
            }
            .padding(14)
        }
        .buttonStyle(.plain)
    }

    // MARK: Плеер

    private var playerGroup: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("ПЛЕЕР").overlineStyle()
            Panel {
                VStack(spacing: 0) {
                    choiceRow(
                        icon: "slider.horizontal.3", title: "Качество",
                        sub: qualityLabel, values: [(0, "Авто"), (1080, "1080p"), (1440, "1440p"), (2160, "4K")],
                        get: { settings.maxQuality }, set: { settings.maxQuality = $0 }
                    )
                    Divider().overlay(Pal.stroke)
                    choiceRow(
                        icon: "film.stack", title: "Заставки и титры",
                        sub: skipLabel, values: [("auto", "Сами"), ("button", "Кнопкой"), ("off", "Смотреть")],
                        get: { settings.skipMode }, set: { settings.skipMode = $0 }
                    )
                    Divider().overlay(Pal.stroke)
                    switchRow(icon: "forward.end.fill", title: "Следующая серия сама", get: { settings.autonext }) { settings.autonext = $0 }
                    Divider().overlay(Pal.stroke)
                    choiceRow(
                        icon: "timer", title: "Прятать панель через",
                        sub: "", values: [(3, "3 с"), (5, "5 с"), (10, "10 с"), (0, "Никогда")],
                        get: { settings.hideAfter }, set: { settings.hideAfter = $0 }
                    )
                    Divider().overlay(Pal.stroke)
                    switchRow(icon: "hand.draw", title: "Свайпы: яркость слева, громкость справа", get: { settings.swipes }) { settings.swipes = $0 }
                    Divider().overlay(Pal.stroke)
                    switchRow(icon: "info.circle", title: "Сводка о потоке всегда", get: { settings.statsAlways }) { settings.statsAlways = $0 }
                }
            }
        }
    }

    private var qualityLabel: String {
        switch settings.maxQuality {
        case 0: return "Авто · до 1080p"
        case 2160: return "4K"
        default: return "\(settings.maxQuality)p"
        }
    }

    private var skipLabel: String {
        switch settings.skipMode {
        case "auto": return "Сами"
        case "off": return "Смотреть"
        default: return "Кнопкой"
        }
    }

    // MARK: Кодеки

    private var codecsGroup: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("КОДЕКИ").overlineStyle()
            Panel {
                VStack(spacing: 0) {
                    choiceRow(
                        icon: "cpu", title: "HEVC", sub: Quality.hevcSupported ? "Устройство умеет" : "Устройство не умеет", values: [("self", "Сам"), ("declare", "Заявлять"), ("off", "Нет")],
                        get: { settings.hevc }, set: { settings.hevc = $0 }
                    )
                    Divider().overlay(Pal.stroke)
                    choiceRow(
                        icon: "cpu", title: "AV1", sub: Quality.av1Supported ? "Устройство умеет" : "Устройство не умеет", values: [("self", "Сам"), ("declare", "Заявлять"), ("off", "Нет")],
                        get: { settings.av1 }, set: { settings.av1 = $0 }
                    )
                }
            }
        }
    }

    // MARK: Загрузки

    private var downloadsGroup: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("ЗАГРУЗКИ").overlineStyle()
            Panel {
                switchRow(icon: "wifi", title: "Только по Wi-Fi", get: { settings.wifiOnly }) { settings.wifiOnly = $0 }
            }
        }
    }

    // MARK: Аккаунт-группа

    private var accountGroup: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("АККАУНТ").overlineStyle()
            Panel {
                VStack(spacing: 0) {
                    // Свой сервер вместо зашитого: у каждого — свой аккаунт и скачанное.
                    serverRow
                    Divider().overlay(Pal.stroke)
                    row(icon: "key", title: "Сменить пароль") { showPasswordDialog = true }
                    Divider().overlay(Pal.stroke)
                    row(icon: "arrow.uturn.left", title: "Выйти", danger: true) { showLogoutConfirm = true }
                }
            }
            Text("Версия \(Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "0.1")")
                .font(.kinoBodySmall)
                .foregroundStyle(Pal.textFaint)
                .padding(.horizontal, 4)
        }
    }

    /// «Сервер · имя» — открывает выбор сервера.
    private var serverRow: some View {
        Button { showServers = true } label: {
            HStack(spacing: 12) {
                Image(systemName: "server.rack").font(.system(size: 17)).frame(width: 24)
                    .foregroundStyle(Pal.textDim)
                Text("Сервер").font(.kinoBody).foregroundStyle(Pal.textMain)
                Spacer()
                Text(ServerConfig.label).font(.kinoBodySmall).foregroundStyle(Pal.textDim).lineLimit(1)
                Image(systemName: "chevron.right").font(.system(size: 13)).foregroundStyle(Pal.textFaint)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 12)
        }
        .buttonStyle(.plain)
    }

    // MARK: Строки настроек

    /// Простая строка без плитки-иконки (аккаунт-группа).
    private func row(icon: String, title: String, danger: Bool = false, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: icon).font(.system(size: 17)).frame(width: 24)
                    .foregroundStyle(danger ? Pal.danger : Pal.textDim)
                Text(title).font(.kinoBody).foregroundStyle(danger ? Pal.danger : Pal.textMain)
                Spacer()
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 12)
        }
        .buttonStyle(.plain)
    }

    private func switchRow(icon: String, title: String, get: @escaping () -> Bool, set: @escaping (Bool) -> Void) -> some View {
        HStack(spacing: 12) {
            Image(systemName: icon).font(.system(size: 17)).foregroundStyle(Pal.textDim).frame(width: 24)
            Text(title).font(.kinoBody).foregroundStyle(Pal.textMain)
            Spacer()
            Toggle("", isOn: Binding(get: {
                get()
            }, set: { v in
                set(v)
                TokenStore.settings = settings
            }))
            .labelsHidden()
            .tint(Pal.accent)
            .fixedSize()
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
    }

    private func choiceRow<T: Equatable & Hashable & CustomStringConvertible>(
        icon: String, title: String, sub: String,
        values: [(T, String)],
        get: @escaping () -> T, set: @escaping (T) -> Void
    ) -> some View {
        HStack(spacing: 12) {
            Image(systemName: icon).font(.system(size: 17)).foregroundStyle(Pal.textDim).frame(width: 24)
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(.kinoBody).foregroundStyle(Pal.textMain)
                if !sub.isEmpty {
                    Text(sub).font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                }
            }
            Spacer()
            Menu {
                ForEach(values, id: \.0) { value, label in
                    Button {
                        set(value)
                        TokenStore.settings = settings
                    } label: {
                        if get() == value {
                            Label(label, systemImage: "checkmark")
                        } else {
                            Text(label)
                        }
                    }
                }
            } label: {
                HStack(spacing: 6) {
                    Text(values.first(where: { $0.0 == get() })?.1 ?? "")
                        .font(.kinoBodyMedium)
                        .foregroundStyle(Pal.accent)
                    Image(systemName: "chevron.up.chevron.down")
                        .font(.system(size: 11))
                        .foregroundStyle(Pal.textFaint)
                }
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
    }

    // MARK: Смена пароля

    private var passwordDialog: some View {
        // Диалог сам меняет пароль; тут только закрыть (второй вызов
        // со сгоревшей сессией выкидывал на вход).
        PasswordChangeDialog { _, _ in
            showPasswordDialog = false
        } onCancel: {
            showPasswordDialog = false
        }
    }
}

struct PasswordChangeDialog: View {
    var onDone: (String, String) -> Void
    var onCancel: () -> Void

    @State private var old = ""
    @State private var new = ""
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        VStack(spacing: 14) {
            Text("Сменить пароль").font(.kinoTitle).foregroundStyle(Pal.textMain)
            SecureField("Старый пароль", text: $old)
                .font(.kinoBody).foregroundStyle(Pal.textMain)
                .padding(12).background(Pal.surface2, in: RoundedRectangle(cornerRadius: 12))
            SecureField("Новый пароль", text: $new)
                .font(.kinoBody).foregroundStyle(Pal.textMain)
                .padding(12).background(Pal.surface2, in: RoundedRectangle(cornerRadius: 12))
            if let error {
                Text(error).font(.kinoBodySmall).foregroundStyle(Pal.danger)
            }
            HStack(spacing: 10) {
                SecondaryButton(title: "Отмена", action: onCancel)
                PrimaryButton(title: "Сохранить", busy: busy) {
                    Task {
                        busy = true
                        do {
                            try await Auth.changePassword(old: old, new: new)
                            onDone(old, new)
                        } catch let e as ApiError {
                            error = e.unauthorized ? "Старый пароль неверен" : "Не получилось"
                        } catch {
                            self.error = "Не получилось"
                        }
                        busy = false
                    }
                }
            }
        }
        .padding(20)
        .presentationDetents([.height(320)])
        .presentationBackground(Pal.surface1)
    }
}
