import SwiftUI

/// «Подключить устройство» — впустить ТВ по коду; 1:1 с LinkDeviceScreen.kt.
struct LinkDeviceScreen: View {
    let app: AppState

    enum Stage: Equatable {
        case entry
        case confirm(DeviceAuthInfo)
        case done
    }

    @Environment(\.dismiss) private var dismiss
    @State private var stage: Stage = .entry
    @State private var code = ""
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        VStack(spacing: 0) {
            header
            ScrollView {
                VStack(spacing: 20) {
                    ZStack {
                        Circle().fill(Pal.accent.opacity(0.14)).frame(width: 88, height: 88)
                        Image(systemName: icon)
                            .font(.system(size: 36))
                            .foregroundStyle(Pal.accent)
                    }
                    switch stage {
                    case .entry: entryForm
                    case let .confirm(info): confirmForm(info)
                    case .done:
                        VStack(spacing: 12) {
                            Text("Готово!").font(.kinoTitle).foregroundStyle(Pal.textMain)
                            Text("Устройство вошло под вашим именем. Можно смотреть.")
                                .font(.kinoBodyMedium).foregroundStyle(Pal.textDim)
                                .multilineTextAlignment(.center)
                            PrimaryButton(title: "Хорошо") { dismiss() }
                        }
                    }
                }
                .padding(20)
            }
        }
        .background(Pal.bg.ignoresSafeArea())
        .navigationBarBackButtonHidden(true)
    }

    private var icon: String {
        switch stage {
        case .entry: return "tv"
        case .confirm: return "questionmark.circle"
        case .done: return "checkmark.circle"
        }
    }

    private var header: some View {
        HStack(spacing: 8) {
            Button {
                if stage == .entry {
                    dismiss()
                } else {
                    withAnimation { stage = .entry }
                }
            } label: {
                Image(systemName: "chevron.left")
                    .font(.system(size: 18, weight: .semibold))
                    .foregroundStyle(Pal.textMain)
                    .frame(width: 44, height: 44)
                    .background(Color.black.opacity(0.4), in: Circle())
            }
            .buttonStyle(.plain)
            Text("Подключить устройство").font(.kinoTitle).foregroundStyle(Pal.textMain)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.top, 8)
    }

    @ViewBuilder
    private var entryForm: some View {
        VStack(spacing: 14) {
            VStack(spacing: 6) {
                Text("Введите код с экрана").font(.kinoTitleMedium).foregroundStyle(Pal.textMain)
                Text("На телевизоре откройте kino — он покажет код из восьми знаков.")
                    .font(.kinoBodySmall).foregroundStyle(Pal.textDim)
                    .multilineTextAlignment(.center)
            }
            TextField("ABCD-EFGH", text: $code)
                .font(.system(size: 28, weight: .bold, design: .monospaced))
                .foregroundStyle(Pal.textMain)
                .multilineTextAlignment(.center)
                .textInputAutocapitalization(.characters)
                .autocorrectionDisabled()
                .onChange(of: code) { _, newValue in
                    var v = newValue.uppercased().filter { $0.isLetter || $0.isNumber }
                    if v.count > 8 { v = String(v.prefix(8)) }
                    if v.count > 4 {
                        let i = v.index(v.startIndex, offsetBy: 4)
                        v = "\(v[..<i])-\(v[i...])"
                    }
                    code = v
                }
                .padding(.vertical, 14)
                .background(Pal.surface2, in: RoundedRectangle(cornerRadius: 18))
            if let error {
                Text(error).font(.kinoBodySmall).foregroundStyle(Pal.danger)
            }
            PrimaryButton(title: "Дальше", busy: busy) { Task { await lookup() } }
                .disabled(alnum.count < 8)
                .opacity(alnum.count < 8 ? 0.5 : 1)
        }
    }

    private var alnum: String { code.filter { $0.isLetter || $0.isNumber } }

    @ViewBuilder
    private func confirmForm(_ info: DeviceAuthInfo) -> some View {
        VStack(spacing: 14) {
            Text("Впустить устройство?").font(.kinoTitleMedium).foregroundStyle(Pal.textMain)
            Panel {
                VStack(spacing: 8) {
                    Text(pretty(alnum))
                        .font(.system(size: 22, weight: .bold, design: .monospaced))
                        .foregroundStyle(Pal.textMain)
                    Text(deviceName(info.device)).font(.kinoBodyMedium).foregroundStyle(Pal.textDim)
                }
                .frame(maxWidth: .infinity)
            }
            Text("Подтверждайте, только если код на экране вашего телевизора. Впуск даёт устройству доступ к вашему профилю.")
                .font(.kinoBodySmall).foregroundStyle(Pal.textFaint)
                .multilineTextAlignment(.center)
            if let error {
                Text(error).font(.kinoBodySmall).foregroundStyle(Pal.danger)
            }
            PrimaryButton(title: "Впустить", busy: busy) { Task { await approve(true) } }
            SecondaryButton(title: "Не впускать") { Task { await approve(false) } }
        }
    }

    private func pretty(_ code: String) -> String {
        guard code.count == 8 else { return code }
        let i = code.index(code.startIndex, offsetBy: 4)
        return "\(code[..<i])-\(code[i...])"
    }

    private func deviceName(_ device: String) -> String {
        switch device {
        case "tv": return "Телевизор"
        case "android", "iphone": return "Телефон"
        default: return device.isEmpty ? "Устройство" : "Браузер"
        }
    }

    private func lookup() async {
        busy = true
        error = nil
        do {
            let info: DeviceAuthInfo = try await Api.shared.get("api/v1/auth/device/\(alnum)")
            withAnimation { stage = .confirm(info) }
        } catch let e as ApiError {
            error = e.offline ? "Нет связи с сервером" : "Код не найден"
        } catch {
            self.error = "Код не найден"
        }
        busy = false
    }

    private func approve(_ yes: Bool) async {
        busy = true
        error = nil
        do {
            if yes {
                try await Api.shared.postNoBody("api/v1/auth/device/\(alnum)/approve")
            } else {
                try await Api.shared.postNoBody("api/v1/auth/device/\(alnum)/deny")
            }
            withAnimation { stage = yes ? .done : .entry }
            code = ""
        } catch let e as ApiError {
            error = e.offline ? "Нет связи с сервером" : "Не получилось"
        } catch {
            self.error = "Не получилось"
        }
        busy = false
    }
}
