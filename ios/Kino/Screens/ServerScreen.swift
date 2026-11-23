import SwiftUI

/// Выбор сервера kino platform: свой адрес вместо зашитого в сборку.
///
/// Адрес проверяется визиткой (GET /api/v1/server) до подключения: сервер ли
/// это вообще и какая на нём регистрация. Недавние серверы — в одно касание:
/// они уже были проверены, а данные каждого лежат в своём профиле (вход там
/// сохранился). Подключение пересобирает приложение под новый сервер.
///
/// `setup` — первый запуск сборки без вшитого сервера: экран стоит вместо
/// входа («Укажите сервер kino platform»), закрыть его нечем — без сервера
/// приложению делать нечего.
struct ServerScreen: View {
    var setup = false

    @Environment(\.dismiss) private var dismiss

    @State private var address = ""
    @State private var checking = false
    /// Проверенный адрес и его визитка; правка поля их сбрасывает.
    @State private var checked: (url: URL, info: ServerInfo)?
    @State private var error: String?
    @State private var recent: [ServerConfig.Known] = ServerConfig.recent
    @FocusState private var focused: Bool

    private var current: URL? { ServerConfig.current }
    private var typed: URL? { ServerAddress.normalize(address) }

    var body: some View {
        ZStack {
            Pal.bg.ignoresSafeArea()
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    if setup { setupHeader } else { header }
                    if let current { currentPanel(current) }
                    addressForm
                    if let checked { infoPanel(checked.url, checked.info) }
                    recentGroup
                }
                .padding(20)
            }
        }
        .preferredColorScheme(.dark)
    }

    // MARK: Шапка

    private var header: some View {
        HStack {
            Text("Сервер").font(.kinoHeadline).foregroundStyle(Pal.textMain)
            Spacer()
            Button("Готово") { dismiss() }
                .font(.kinoLabel)
                .foregroundStyle(Pal.accent)
                .buttonStyle(.plain)
        }
    }

    private var setupHeader: some View {
        VStack(alignment: .leading, spacing: 10) {
            BrandMark(tile: 44, icon: 30, wordFont: .kinoHeadline)
                .padding(.top, 24)
            Text("Укажите сервер kino platform").font(.kinoHeadline).foregroundStyle(Pal.textMain)
            Text("Адрес даст тот, кто поднял сервер: например, kino.example.org. Войти или зарегистрироваться можно будет на следующем шаге.")
                .font(.kinoBodyMedium)
                .foregroundStyle(Pal.textDim)
        }
    }

    private func currentPanel(_ current: URL) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Сейчас").overlineStyle()
            Panel {
                VStack(alignment: .leading, spacing: 2) {
                    Text(ServerConfig.label(for: current)).font(.kinoTitleSmall).foregroundStyle(Pal.textMain)
                    Text(current.absoluteString).font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                }
            }
            Text("У каждого сервера свой аккаунт, история и скачанное — при смене ничего не теряется.")
                .font(.kinoBodySmall)
                .foregroundStyle(Pal.textFaint)
                .padding(.horizontal, 4)
        }
    }

    // MARK: Ввод адреса

    private var addressForm: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(current == nil ? "Адрес сервера" : "Другой сервер").overlineStyle()
            HStack(spacing: 10) {
                Image(systemName: "server.rack").font(.system(size: 16)).foregroundStyle(Pal.textDim)
                TextField("kino.example.org", text: $address)
                    .keyboardType(.URL)
                    .textContentType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .submitLabel(.go)
                    .focused($focused)
                    .onSubmit { Task { await check() } }
                    .font(.kinoBody)
                    .foregroundStyle(Pal.textMain)
            }
            .padding(.horizontal, 14)
            .frame(height: 52)
            .background(Pal.surface2, in: RoundedRectangle(cornerRadius: 16))
            .onChange(of: address) { _, _ in
                checked = nil
                error = nil
            }

            if let url = checked?.url ?? typed, ServerAddress.isInsecure(url), ServerAddress.allowsCleartext(url) {
                Label {
                    Text("Соединение не шифруется (http): пароль и история видны всем в вашей сети. Годится только для домашней сети.")
                } icon: {
                    Image(systemName: "lock.open")
                }
                .font(.kinoBodySmall)
                .foregroundStyle(Pal.gold)
            }
            if let error {
                Text(error).font(.kinoBodySmall).foregroundStyle(Pal.danger)
            }

            if let checked {
                PrimaryButton(title: "Подключиться", icon: "arrow.right.circle") {
                    connect(checked.url, checked.info)
                }
            } else {
                PrimaryButton(title: "Проверить", busy: checking) {
                    Task { await check() }
                }
                .opacity(address.trimmingCharacters(in: .whitespaces).isEmpty ? 0.5 : 1)
                .disabled(address.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
    }

    private func infoPanel(_ url: URL, _ info: ServerInfo) -> some View {
        Panel {
            HStack(alignment: .top, spacing: 12) {
                Image(systemName: "checkmark.seal.fill").font(.system(size: 22)).foregroundStyle(Pal.accent)
                VStack(alignment: .leading, spacing: 3) {
                    Text(info.name ?? ServerAddress.hostLabel(url)).font(.kinoTitleSmall).foregroundStyle(Pal.textMain)
                    Text(url.absoluteString).font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                    Text(Self.registrationText(info.reg)).font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                }
            }
        }
    }

    static func registrationText(_ reg: ServerInfo.Registration) -> String {
        switch reg {
        case .invite: return "Регистрация по коду приглашения"
        case .open: return "Открытая регистрация"
        case .closed: return "Регистрации нет — аккаунт выдаёт администратор"
        }
    }

    // MARK: Недавние

    @ViewBuilder
    private var recentGroup: some View {
        let others = recent.filter { $0.url != current?.absoluteString }
        // Вшитый в сборку сервер — всегда под рукой (если он есть).
        let builtIn = ServerConfig.defaultURL.flatMap { url in
            current == url || others.contains(where: { $0.url == url.absoluteString }) ? nil : url
        }
        if !others.isEmpty || builtIn != nil {
            VStack(alignment: .leading, spacing: 10) {
                Text("Недавние").overlineStyle()
                VStack(spacing: 0) {
                    ForEach(Array(others.enumerated()), id: \.element.id) { i, known in
                        if i > 0 { Divider().overlay(Pal.stroke).padding(.leading, 52) }
                        recentRow(known)
                    }
                    if let builtIn {
                        if !others.isEmpty { Divider().overlay(Pal.stroke).padding(.leading, 52) }
                        serverRow(
                            title: "Сервер по умолчанию",
                            sub: ServerAddress.hostLabel(builtIn),
                            icon: "house"
                        ) { connect(builtIn, nil) }
                    }
                }
                .background(Pal.surface1, in: RoundedRectangle(cornerRadius: 18))
                .overlay(RoundedRectangle(cornerRadius: 18).stroke(Pal.stroke))
            }
        }
    }

    @ViewBuilder
    private func recentRow(_ known: ServerConfig.Known) -> some View {
        if let url = known.address {
            let isDefault = url == ServerConfig.defaultURL
            serverRow(
                title: known.name ?? ServerAddress.hostLabel(url) + (isDefault ? " (по умолчанию)" : ""),
                sub: url.absoluteString,
                icon: isDefault ? "house" : "server.rack",
                onForget: isDefault ? nil : {
                    ServerConfig.forget(url)
                    recent = ServerConfig.recent
                }
            ) { connect(url, nil) }
        }
    }

    private func serverRow(title: String, sub: String, icon: String, onForget: (() -> Void)? = nil, action: @escaping () -> Void) -> some View {
        HStack(spacing: 12) {
            Button(action: action) {
                HStack(spacing: 12) {
                    Image(systemName: icon).font(.system(size: 17)).foregroundStyle(Pal.textDim).frame(width: 28)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(title).font(.kinoBody).foregroundStyle(Pal.textMain).lineLimit(1)
                        Text(sub).font(.kinoBodySmall).foregroundStyle(Pal.textDim).lineLimit(1)
                    }
                    Spacer()
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if let onForget {
                Button(action: onForget) {
                    Image(systemName: "xmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Pal.textFaint)
                        .frame(width: 32, height: 32)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Убрать из недавних")
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
    }

    // MARK: Действия

    private func check() async {
        guard !checking else { return }
        guard let url = typed else {
            error = "Не похоже на адрес сервера"
            return
        }
        focused = false
        checking = true
        error = nil
        defer { checking = false }
        do {
            var target = url
            let info: ServerInfo
            do {
                info = try await ServerProbe.fetch(url)
            } catch ServerProbe.Failure.unreachable where !address.contains("://") {
                // Схему не указали, а по https не ответил: свой сервер в домашней
                // сети часто живёт по http — пробуем его (с предупреждением).
                // Только для хоста домашней сети: в интернет — только https.
                guard let http = ServerAddress.normalize("http://" + address.trimmingCharacters(in: .whitespacesAndNewlines)),
                      ServerAddress.allowsCleartext(http)
                else { throw ServerProbe.Failure.unreachable }
                info = try await ServerProbe.fetch(http)
                target = http
            }
            // Пока шла проверка, поле могли поменять — визитка не к нему.
            guard typed == url else { return }
            checked = (target, info)
        } catch let f as ServerProbe.Failure {
            error = f.message
        } catch {
            self.error = ServerProbe.Failure.unreachable.message
        }
    }

    private func connect(_ url: URL, _ info: ServerInfo?) {
        if !setup { dismiss() }
        Task { await ServerSwitch.apply(url, info: info) }
    }
}

/// Строка «Сервер: <имя> · Сменить» — на экране входа.
struct ServerLine: View {
    var label: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                Image(systemName: "server.rack").font(.system(size: 13))
                Text("Сервер: \(label)").lineLimit(1)
                Text("· Сменить").foregroundStyle(Pal.accent)
            }
            .font(.kinoBodySmall)
            .foregroundStyle(Pal.textDim)
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(Pal.surface1, in: Capsule())
        }
        .buttonStyle(.plain)
    }
}
