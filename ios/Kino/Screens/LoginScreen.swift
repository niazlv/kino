import SwiftUI
import CoreImage.CIFilterBuiltins

/// Вход: пароль или код с другого устройства — 1:1 с LoginScreen.kt.
///
/// Сервер выбирается здесь же («Сервер: … · Сменить»), а регистрация — по
/// его визитке: по приглашению (по умолчанию), открытая (код необязателен)
/// или никакой (аккаунт выдаёт администратор).
struct LoginScreen: View {
    let app: AppState

    enum Mode { case password, register, code }
    @State private var mode: Mode = .password
    @State private var login = ""
    @State private var password = ""
    @State private var invite = ""
    @State private var displayName = ""
    @State private var showPassword = false
    @State private var busy = false
    @State private var error: String?
    /// Визитка выбранного сервера; пока не пришла (или сервер старый) — как
    /// раньше, по приглашению.
    @State private var server: ServerInfo?
    @State private var serverLabel = ServerConfig.label
    @State private var showServers = false

    private var registration: ServerInfo.Registration { server?.reg ?? .invite }

    var body: some View {
        ZStack(alignment: .topLeading) {
            Pal.bg.ignoresSafeArea()
            // Радиальное свечение акцентом в левом верхнем углу.
            RadialGradient(colors: [Pal.accent.opacity(0.16), .clear], center: .topLeading, startRadius: 0, endRadius: 700)
                .ignoresSafeArea()
            ScrollView {
                VStack(spacing: 16) {
                    VStack(spacing: 8) {
                        BrandMark(tile: 56, icon: 40, wordFont: .kinoDisplay)
                        Text("Домашний кинотеатр для своих")
                            .font(.kinoBodyMedium)
                            .foregroundStyle(Pal.textDim)
                        ServerLine(label: serverLabel) { showServers = true }
                            .padding(.top, 6)
                    }
                    .padding(.bottom, 12)

                    switch mode {
                    case .password, .register: passwordForm
                    case .code: DeviceCodeLogin(app: app, onPassword: { withAnimation { mode = .password } })
                    }
                }
                .padding(.horizontal, 24)
                .frame(maxWidth: 420)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 40)
            }
        }
        .task { await loadServerInfo() }
        .sheet(isPresented: $showServers) { ServerScreen() }
    }

    /// Визитка выбранного сервера: имя для строки и режим регистрации.
    private func loadServerInfo() async {
        guard let url = ServerConfig.current, let info = try? await ServerProbe.fetch(url) else { return }
        server = info
        ServerConfig.remember(url, info: info)
        serverLabel = ServerConfig.label(for: url)
        // Регистрацию закрыли, а форма уже на ней — назад ко входу.
        if info.reg == .closed, mode == .register { mode = .password }
    }

    @ViewBuilder
    private var passwordForm: some View {
        let register = mode == .register
        VStack(spacing: 12) {
            if register {
                field(registration == .open ? "Код приглашения, если есть" : "Код приглашения", text: $invite, icon: "envelope.badge")
            }
            field("Логин", text: $login, icon: "person")
            field("Пароль", text: $password, icon: "lock", secure: !showPassword, trailing: eyeButton)
            if register {
                field("Как вас звать", text: $displayName, icon: "textformat")
            }
            if let error {
                Text(error)
                    .font(.kinoBodySmall)
                    .foregroundStyle(Pal.danger)
                    .multilineTextAlignment(.center)
            }
            PrimaryButton(
                title: register ? "Зарегистрироваться" : "Войти",
                busy: busy
            ) {
                Task { await submit(register: register) }
            }
            SecondaryButton(title: "Войти кодом с другого устройства", icon: "qrcode") {
                withAnimation { mode = .code }
            }
            if register || registration != .closed {
                Button(register ? "Уже есть аккаунт? Войти"
                       : registration == .open ? "Нет аккаунта? Зарегистрироваться" : "Есть приглашение? Зарегистрироваться") {
                    withAnimation { mode = register ? .password : .register }
                    error = nil
                }
                .font(.kinoBodyMedium)
                .foregroundStyle(Pal.accent)
                .buttonStyle(.plain)
            }
        }
    }

    private var eyeButton: AnyView {
        AnyView(
            Button {
                showPassword.toggle()
            } label: {
                Image(systemName: showPassword ? "eye.slash" : "eye")
                    .font(.system(size: 16))
                    .foregroundStyle(Pal.textDim)
            }
            .buttonStyle(.plain)
        )
    }

    private func field(
        _ placeholder: String, text: Binding<String>, icon: String,
        secure: Bool = false, trailing: AnyView? = nil
    ) -> some View {
        HStack(spacing: 10) {
            Image(systemName: icon).font(.system(size: 16)).foregroundStyle(Pal.textDim)
            Group {
                if secure {
                    SecureField(placeholder, text: text)
                } else {
                    TextField(placeholder, text: text)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
            }
            .font(.kinoBody)
            .foregroundStyle(Pal.textMain)
            if let trailing { trailing.anyView }
        }
        .padding(.horizontal, 14)
        .frame(height: 52)
        .background(Pal.surface2, in: RoundedRectangle(cornerRadius: 16))
    }

    private func submit(register: Bool) async {
        busy = true
        error = nil
        do {
            if register {
                try await Auth.register(invite: invite.trimmingCharacters(in: .whitespaces), login: login, password: password, displayName: displayName)
            } else {
                try await Auth.login(login: login, password: password)
            }
            await app.onLoginDone()
        } catch let e as ApiError {
            switch true {
            case e.offline: error = "Нет связи с сервером"
            case register && e.status == 403: error = "Регистрация на этом сервере закрыта"
            case register && e.status == 404: error = "Код приглашения не подходит"
            case register && e.status == 410: error = "Срок приглашения истёк"
            case register && e.status == 400: error = "Логин или пароль не подходят: пароль — не короче 10 символов"
            case e.code == "bad_invite": error = "Приглашение не подходит"
            case register && e.status == 409: error = "Такой логин уже занят (или приглашение уже использовано)"
            case e.code == "taken": error = "Такой логин уже занят"
            case e.unauthorized: error = "Неверный логин или пароль"
            case e.status == 429: error = "Слишком много попыток — подождите"
            default: error = "Не удалось войти (\(e.status))"
            }
        } catch {
            self.error = "Не удалось войти"
        }
        busy = false
    }
}

/// Вход по коду устройства: показываем код, ждём подтверждения.
struct DeviceCodeLogin: View {
    let app: AppState
    var onPassword: () -> Void

    @State private var begin: DeviceBegin?
    @State private var runId = 0
    @State private var failed = false

    var body: some View {
        VStack(spacing: 16) {
            Text("Код для входа")
                .font(.kinoTitleMedium)
                .foregroundStyle(Pal.textMain)
            if let begin {
                Text(pretty(begin.userCode))
                    .font(.system(size: 34, weight: .bold, design: .monospaced))
                    .foregroundStyle(Pal.accent)
                    .padding(.horizontal, 24)
                    .padding(.vertical, 14)
                    .background(Pal.surface2, in: RoundedRectangle(cornerRadius: 12))
                HStack(spacing: 10) {
                    SpinnerView(size: 18)
                    Text("Жду подтверждения…").font(.kinoBodyMedium).foregroundStyle(Pal.textDim)
                }
                if let qr = qrImage(begin.verificationUrl) {
                    qr
                        .resizable().interpolation(.none).scaledToFit()
                        .frame(width: 112, height: 112)
                        .padding(14)
                        .background(.white, in: RoundedRectangle(cornerRadius: 10))
                    Text("Или наведите камеру телефона…")
                        .font(.kinoBodySmall)
                        .foregroundStyle(Pal.textFaint)
                }
            } else if failed {
                Text("Не удалось получить код").font(.kinoBodyMedium).foregroundStyle(Pal.danger)
                SecondaryButton(title: "Повторить", icon: "arrow.clockwise") { runId += 1 }
            } else {
                SpinnerView(size: 28)
            }
            DeviceCodeSteps()
            SecondaryButton(title: "Войти паролем", action: onPassword)
        }
        // Один запуск на показ; повтор и новый код — сменой runId.
        .task(id: runId) { await run() }
    }

    private func run() async {
        failed = false
        begin = nil
        do {
            let pair = try await Auth.deviceLogin { begin in
                self.begin = begin
            }
            Api.shared.saveSession(pair)
            await app.onLoginDone()
        } catch {
            // Ушли с экрана / перезапуск — отменённый запрос не ошибка.
            if Task.isCancelled { return }
            if let e = error as? ApiError, e.code == "expired_token" {
                // Истёкший код меняем на новый сами.
                runId += 1
            } else {
                failed = true
            }
        }
    }

    private func pretty(_ code: String) -> String {
        guard code.count == 8 else { return code }
        let i = code.index(code.startIndex, offsetBy: 4)
        return "\(code[..<i])-\(code[i...])"
    }

    private func qrImage(_ text: String) -> Image? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        filter.correctionLevel = "M"
        guard let out = filter.outputImage else { return nil }
        let scaled = out.transformed(by: CGAffineTransform(scaleX: 8, y: 8))
        guard let cg = CIContext().createCGImage(scaled, from: scaled.extent) else { return nil }
        return Image(uiImage: UIImage(cgImage: cg))
    }
}

/// Нумерованные шаги 1-2-3, как DeviceCodeSteps.
struct DeviceCodeSteps: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            step(1, "Откройте kino на телефоне или компьютере")
            step(2, "Войдите под своим именем")
            step(3, "Профиль → «Подключить устройство», введите код")
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Pal.surface1, in: RoundedRectangle(cornerRadius: 14))
    }

    private func step(_ n: Int, _ text: String) -> some View {
        HStack(alignment: .top, spacing: 10) {
            ZStack {
                Circle().fill(Pal.accent.opacity(0.15)).frame(width: 22, height: 22)
                Text(String(n)).font(.system(size: 12, weight: .bold)).foregroundStyle(Pal.accent)
            }
            Text(text).font(.kinoBodySmall).foregroundStyle(Pal.textDim)
        }
    }
}
