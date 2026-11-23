import AVKit
import MediaPlayer
import SwiftUI

/// Плеер — 1:1 с PlayerScreen.kt («cine»-раскладка kinopub, палитра Kp*).
struct PlayerScreen: View {
    @Bindable var model: PlayerModel
    var onClose: () -> Void

    // Палитра cine — отдельная от темы приложения.
    private let kpGreen = Color(hex: 0x35C56A)
    private let kpGreenDark = Color(hex: 0x1E8A47)
    private let kpPlate = Color(hex: 0x000000, alpha: 0xBD)
    private let kpMenuBg = Color(hex: 0x101218, alpha: 0xFA)
    private let kpMenuBorder = Color(hex: 0xFFFFFF, alpha: 0x24)
    private let kpSeg = Color(hex: 0xFFD666, alpha: 0xD9)

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            VideoLayerView(player: model.player)
                .ignoresSafeArea()
            gestureLayer
            if panelVisible {
                controls
                    .transition(.opacity)
            }
            skipButton
            if let seg = model.autoSkippedSegment {
                autoSkipToast(seg)
            }
            if model.statsVisible, panelVisible {
                statsOverlay
            }
            switch model.phase {
            case .ended: endOverlay
            case .failed(let message): errorOverlay(message)
            case .preparing, .buffering: spinnerOverlay
            default: EmptyView()
            }
            if let menu = model.menu {
                popMenu(menu)
            }
        }
        .statusBarHidden(true)
        .persistentSystemOverlays(.hidden)
        // Цикл живёт, пока экран на месте: .task отменяется при уходе.
        .task { await autoHideLoop() }
    }

    private var panelVisible: Bool {
        model.panelVisible
    }

    // MARK: Жесты

    private var gestureLayer: some View {
        Color.clear
            .contentShape(Rectangle())
            .gesture(
                DragGesture(minimumDistance: 12)
                    .onChanged { value in
                        handleDrag(value)
                    }
                    .onEnded { value in
                        endDrag(value)
                    }
            )
            // Одно распознавание касаний: одиночное — панель (с задержкой,
            // чтобы не мигать при двойном), двойное — ±15 с / пауза. Связка
            // onTapGesture + SpatialTapGesture(count: 2) одиночное не ловила.
            .simultaneousGesture(
                SpatialTapGesture()
                    .onEnded { value in handleTap(value.location) }
            )
            .overlay(alignment: .center) {
                if let flash {
                    flashBadge(flash)
                }
            }
            .overlay(alignment: .center) {
                scrubTooltip
            }
    }

    @State private var lastTapAt: Date?
    @State private var pendingTap: Task<Void, Never>?

    private func handleTap(_ location: CGPoint) {
        let now = Date()
        if let last = lastTapAt, now.timeIntervalSince(last) < 0.3 {
            pendingTap?.cancel()
            pendingTap = nil
            lastTapAt = nil
            doubleTap(location)
            return
        }
        lastTapAt = now
        pendingTap?.cancel()
        pendingTap = Task {
            try? await Task.sleep(nanoseconds: 280_000_000)
            guard !Task.isCancelled else { return }
            withAnimation(.easeInOut(duration: 0.15)) {
                model.panelVisible.toggle()
                model.touchInput()
            }
        }
    }

    private func doubleTap(_ location: CGPoint) {
        let w = UIScreen.main.bounds.width
        if location.x < w * 0.36 {
            model.skip(-15)
            flash = .minus
        } else if location.x > w * 0.64 {
            model.skip(15)
            flash = .plus
        } else {
            model.togglePlay()
        }
        withAnimation(.easeOut(duration: 0.3)) { flash = nil }
    }

    enum Flash: Equatable { case minus, plus }
    @State private var flash: Flash?
    @State private var scrub: ScrubState?

    struct ScrubState {
        var start: Double
        var span: Double
        var current: Double
    }

    private func handleDrag(_ value: DragGesture.Value) {
        // Вертикальные свайпы: лево — яркость, право — громкость.
        if model.settings.swipes, abs(value.translation.height) > abs(value.translation.width) {
            let w = UIScreen.main.bounds.width
            if value.startLocation.x < w * 0.4 {
                // Отсчёт — от яркости в начале жеста, а не от нуля.
                if swipeStart == nil { swipeStart = Date(); startBrightness = UIScreen.main.brightness }
                brightnessDelta = -value.translation.height / 600
                UIScreen.main.brightness = min(max(startBrightness + brightnessDelta, 0), 1)
            } else if value.startLocation.x > w * 0.6 {
                if swipeStart == nil { swipeStart = Date(); VolumeController.shared.begin() }
                VolumeController.shared.setRelative(-value.translation.height / 600)
            }
            return
        }
        // Горизонтальный драг — скраб.
        guard model.duration > 0 else { return }
        if scrub == nil {
            scrub = ScrubState(start: model.position, span: min(max(model.duration / 4, 120), 900), current: model.position)
        }
        let delta = Double(value.translation.width / UIScreen.main.bounds.width) * scrub!.span
        scrub!.current = min(max(0, scrub!.start + delta), model.duration)
        model.position = scrub!.current
    }

    private func endDrag(_ value: DragGesture.Value) {
        if let s = scrub {
            model.seek(s.current)
            scrub = nil
        }
        brightnessDelta = 0
        swipeStart = nil
    }

    @State private var brightnessDelta: Double = 0
    @State private var startBrightness: Double = 0
    @State private var swipeStart: Date?

    @ViewBuilder
    private var scrubTooltip: some View {
        if let s = scrub {
            VStack(spacing: 4) {
                Text(fmtTime(s.current))
                    .font(.system(size: 15, weight: .semibold, design: .monospaced))
                    .foregroundStyle(.white)
                HStack(spacing: 4) {
                    Image(systemName: s.current > s.start ? "arrow.right" : "arrow.left")
                        .font(.system(size: 12))
                        .foregroundStyle(kpGreen)
                    Text("\(fmtTime(abs(s.current - s.start)))")
                        .font(.system(size: 12))
                        .foregroundStyle(.white.opacity(0.8))
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
            .background(Color.black.opacity(0.6), in: RoundedRectangle(cornerRadius: 8))
        }
    }

    private func flashBadge(_ f: Flash) -> some View {
        VStack(spacing: 6) {
            Image(systemName: f == .minus ? "gobackward.15" : "goforward.15")
                .font(.system(size: 44))
                .foregroundStyle(.white)
            Text(f == .minus ? "−15 с" : "+15 с")
                .font(.kinoBodyMedium)
                .foregroundStyle(.white)
        }
        .padding(24)
        .background(Color.white.opacity(0.12), in: Circle())
    }

    // MARK: Панель

    private var controls: some View {
        VStack(spacing: 0) {
            topBar
            Spacer()
            bottomBar
        }
        .background(.clear)
    }

    private var topBar: some View {
        VStack(spacing: 8) {
            HStack(spacing: 12) {
                Button {
                    onClose()
                } label: {
                    Image(systemName: "chevron.left")
                        .font(.system(size: 20, weight: .semibold))
                        .foregroundStyle(.white)
                        .frame(width: 44, height: 44)
                }
                .buttonStyle(.plain)
                VStack(spacing: 2) {
                    Text(titleLine.uppercased())
                        .font(.system(size: 15, weight: .semibold))
                        .foregroundStyle(.white.opacity(0.9))
                        .lineLimit(1)
                    if let epTitle = episodeTitle {
                        Text(epTitle)
                            .font(.system(size: 14))
                            .foregroundStyle(.white.opacity(0.7))
                            .lineLimit(1)
                    }
                }
                Spacer()
            }
            HStack(spacing: 8) {
                pill("play.rectangle", value: qualityLabel, menu: .video)
                pill("speaker.wave.2", value: model.audioLabel.isEmpty ? "Аудио" : model.audioLabel, menu: .audio)
                pill("captions.bubble", value: model.subLabel, menu: .subs)
                Spacer()
            }
            .padding(.leading, 52)
        }
        .padding(.horizontal, 8)
        .padding(.top, 6)
        .padding(.bottom, 10)
        .background(kpPlate.ignoresSafeArea(edges: .top))
    }

    /// Пилюля дорожки как KpPill на Android: значок + текущее значение
    /// заглавными в одну строку (оверлеем с отступом внутри пилюли значение
    /// обрезалось бы или переносилось).
    private func pill(_ icon: String, value: String, menu: PlayerModel.MenuKind) -> some View {
        Button {
            model.menu = model.menu == menu ? nil : menu
            model.touchInput()
        } label: {
            CappedWidth(max: 300) {
            HStack(spacing: 8) {
                Image(systemName: icon)
                    .font(.system(size: 15))
                    .foregroundStyle(.white.opacity(0.8))
                Text(value.uppercased())
                    .font(.system(size: 14, weight: .semibold))
                    .tracking(0.4)
                    .foregroundStyle(kpGreen)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            }
            .background(
                RoundedRectangle(cornerRadius: 8)
                    .fill(model.menu == menu ? Color.white.opacity(0.15) : .clear)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .stroke(model.menu == menu ? kpGreen : Color.white.opacity(0.4), lineWidth: 1.5)
            )
        }
        .buttonStyle(.plain)
    }

    private var bottomBar: some View {
        VStack(spacing: 6) {
            HStack(spacing: 10) {
                Text(fmtTime(model.position))
                    .font(.system(size: 13, design: .monospaced))
                    .foregroundStyle(.white.opacity(0.9))
                    .frame(minWidth: 56, alignment: .trailing)
                SeekBar(
                    position: model.position,
                    duration: model.duration,
                    buffered: model.buffered,
                    segments: model.skips,
                    trackColor: Color.white.opacity(0.3),
                    bufferedColor: Color.white.opacity(0.34),
                    playedColor: kpGreen,
                    segColor: kpSeg
                ) { target in
                    model.seek(target)
                }
                Text(fmtTime(model.duration))
                    .font(.system(size: 13, design: .monospaced))
                    .foregroundStyle(.white.opacity(0.7))
            }
            .padding(.horizontal, 16)
            HStack {
                playerButton("backward.end.fill") {
                    if let prev = model.prevEpisode { model.play(prev) } else { model.seek(0) }
                }
                .disabled(model.prevEpisode == nil)
                playerButton("gobackward.15") { model.skip(-15) }
                playerButton(model.phase == .playing ? "pause.fill" : "play.fill", big: true) {
                    model.togglePlay()
                }
                playerButton("goforward.15") { model.skip(15) }
                playerButton("forward.end.fill") {
                    if let next = model.nextEpisode { model.play(next) }
                }
                .disabled(model.nextEpisode == nil)
                playerButton("speedometer") {
                    model.menu = model.menu == .speed ? nil : .speed
                }
                .foregroundStyle(model.menu == .speed ? kpGreen : .white)
                playerButton("info.circle") {
                    model.statsVisible.toggle()
                }
                .foregroundStyle(model.statsVisible ? kpGreen : .white)
            }
            .frame(maxWidth: 560)
            .padding(.bottom, 10)
        }
        .padding(.top, 8)
        .background(kpPlate.ignoresSafeArea(edges: .bottom))
    }

    private func playerButton(_ icon: String, big: Bool = false, action: @escaping () -> Void) -> some View {
        Button {
            action()
            model.touchInput()
        } label: {
            Image(systemName: icon)
                .font(.system(size: big ? 26 : 20))
                .foregroundStyle(.white)
                .frame(width: big ? 56 : 44, height: 44)
        }
        .buttonStyle(.plain)
    }

    // MARK: Меню

    @ViewBuilder
    private func popMenu(_ kind: PlayerModel.MenuKind) -> some View {
        VStack {
            Spacer()
            HStack {
                if kind == .speed { Spacer() }
                PopMenuView(model: model, kind: kind, green: kpGreen, greenDark: kpGreenDark)
                    .frame(width: 260)
                if kind != .speed { Spacer() }
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 150)
        }
        .contentShape(Rectangle())
        .onTapGesture {
            model.menu = nil
        }
    }

    struct PopMenuView: View {
        @Bindable var model: PlayerModel
        let kind: PlayerModel.MenuKind
        let green: Color
        let greenDark: Color

        var body: some View {
            VStack(alignment: .leading, spacing: 2) {
                switch kind {
                case .video:
                    menuHeader("КАЧЕСТВО")
                    menuItem("Авто", selected: model.qualityMax == 0) { model.setQuality(0) }
                    ForEach(model.playback?.renditions ?? [], id: \.height) { r in
                        menuItem("\(r.height)p", hint: r.codec, selected: model.qualityMax == r.height) {
                            model.setQuality(r.height)
                        }
                    }
                case .audio:
                    menuHeader("ОЗВУЧКА")
                    let voices = model.voiceOptions
                    if voices.isEmpty {
                        Text("Дорожка одна, выбирать не из чего")
                            .font(.system(size: 14))
                            .foregroundStyle(.white.opacity(0.6))
                            .padding(.vertical, 8)
                    } else {
                        ForEach(voices) { v in
                            menuItem(v.title, selected: model.audioLabel == v.title || model.choiceVoice == v.key) {
                                model.setAudio(variantLabel: v.title, voice: v.key)
                            }
                        }
                    }
                case .subs:
                    menuHeader("СУБТИТРЫ")
                    menuItem("Выключены", selected: model.subLabel == "Выкл") {
                        model.setSubtitle("Выкл")
                    }
                    ForEach(model.subOptions, id: \.self) { s in
                        menuItem(s, selected: model.subLabel == s) {
                            model.setSubtitle(s)
                        }
                    }
                case .speed:
                    menuHeader("СКОРОСТЬ")
                    ForEach(speedItems, id: \.value) { item in
                        menuItem(item.label, selected: model.speed == item.value) {
                            model.setSpeed(item.value)
                        }
                    }
                }
            }
            .padding(6)
            .background(
                RoundedRectangle(cornerRadius: 8)
                    .fill(Color(hex: 0x101218, alpha: 0xFA))
                    .shadow(color: .black.opacity(0.5), radius: 12)
            )
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.white.opacity(0.14)))
        }

        private let speedItems: [(value: Float, label: String)] = [
            (0.5, "0.50x"), (0.75, "0.75x"), (1.0, "Обычная"), (1.25, "1.25x"), (1.5, "1.50x"), (2.0, "2.00x"),
        ]

        private func menuHeader(_ text: String) -> some View {
            Text(text)
                .font(.system(size: 11, weight: .bold))
                .tracking(1)
                .foregroundStyle(.white.opacity(0.45))
                .padding(.horizontal, 8)
                .padding(.top, 6)
        }

        private func menuItem(_ label: String, hint: String? = nil, selected: Bool, action: @escaping () -> Void) -> some View {
            Button(action: action) {
                HStack(spacing: 8) {
                    Image(systemName: "checkmark")
                        .font(.system(size: 13, weight: .bold))
                        .foregroundStyle(selected ? green : .clear)
                        .frame(width: 26)
                    Text(label)
                        .font(.system(size: 15))
                        .foregroundStyle(selected ? green : .white)
                    Spacer()
                    if let hint {
                        Text(hint).font(.system(size: 12)).foregroundStyle(.white.opacity(0.4))
                    }
                }
                .padding(.vertical, 9)
                .padding(.horizontal, 6)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
        }
    }

    // MARK: Статистика

    private var statsOverlay: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("\(endTimeText) до конца").font(.system(size: 12)).foregroundStyle(.white.opacity(0.8))
            }
            ForEach(model.statsLines, id: \.self) { line in
                Text(line).font(.system(size: 12)).foregroundStyle(.white.opacity(0.8))
            }
        }
        .padding(10)
        .background(Color(hex: 0x141414, alpha: 0xB3), in: RoundedRectangle(cornerRadius: 10))
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topTrailing)
        .padding(.trailing, 12)
        .padding(.top, 110)
    }

    private var endTimeText: String {
        guard model.duration > 0 else { return "" }
        let remaining = (model.duration - model.position) / Double(max(model.speed, 0.1))
        let f = DateFormatter()
        f.dateFormat = "HH:mm"
        return f.string(from: Date().addingTimeInterval(remaining))
    }

    /// Подпись качества по кадру (qualityLabel на Android): 1920×800 — это
    /// «1080p», выбранное руками — без «авто».
    private var qualityLabel: String {
        if model.qualityMax > 0 { return "\(model.qualityMax)p" }
        let size = model.player.currentItem?.presentationSize ?? .zero
        let w = Int(size.width), h = Int(size.height)
        let step: Int
        switch true {
        case w >= 3800 || h >= 2000: step = 2160
        case w >= 2500 || h >= 1400: step = 1440
        case w >= 1880 || h >= 1000: step = 1080
        case w >= 1260 || h >= 700: step = 720
        case w >= 840 || h >= 460: step = 480
        default: step = h
        }
        return step > 0 ? "\(step)p (авто)" : "Авто"
    }

    private var titleLine: String {
        var parts: [String] = []
        if let d = model.detail {
            parts.append(splitTitle(d.title.title).0)
            if let ep = model.detail?.episodes.first(where: { $0.id == model.episodeId }) {
                if ep.seasonNo > 0 { parts.append("S\(ep.seasonNo)E\(ep.episodeNo)") }
                if d.title.year > 0 { parts.append("(\(d.title.year))") }
            }
        }
        return parts.joined(separator: " ")
    }

    private var episodeTitle: String? {
        guard let ep = model.detail?.episodes.first(where: { $0.id == model.episodeId }) else { return nil }
        if let t = ep.title, !t.isBlank, !t.hasPrefix("серия") { return t }
        return nil
    }

    // MARK: Скипы

    private var skipButton: some View {
        Group {
            if case .playing = model.phase, let active = Skips.active(model.skips, at: model.position),
               settingsSkipMode != "off" {
                let auto = settingsSkipMode == "auto" && Skips.autoSeekable(active)
                if !auto {
                    Button {
                        model.seek(active.end)
                    } label: {
                        Text(Skips.kind(active.kind)?.label ?? "Пропустить")
                            .font(.system(size: 14, weight: .semibold))
                            .foregroundStyle(.white)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 10)
                            .background(Color(hex: 0x101010, alpha: 0xBD), in: RoundedRectangle(cornerRadius: 6))
                            .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.white.opacity(0.6)))
                    }
                    .buttonStyle(.plain)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
                    .padding(.trailing, 16)
                    .padding(.bottom, panelVisible ? 150 : 40)
                }
            }
        }
    }

    private var settingsSkipMode: String { model.settings.skipMode }

    private func autoSkipToast(_ seg: SkipSegment) -> some View {
        VStack(spacing: 8) {
            Text(Skips.kind(seg.kind)?.skippedLabel ?? "Заставка пропущена")
                .font(.kinoBodySmall)
                .foregroundStyle(.white.opacity(0.8))
            Button {
                model.undoAutoSkip(seg)
            } label: {
                Text("Вернуть")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(.white)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 7)
                    .background(Color(hex: 0x101010, alpha: 0xBD), in: RoundedRectangle(cornerRadius: 6))
            }
            .buttonStyle(.plain)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
        .padding(.trailing, 16)
        .padding(.bottom, panelVisible ? 150 : 40)
    }

    // MARK: Конец серии / ошибка

    private var endOverlay: some View {
        ZStack {
            Color.black.opacity(0.62).ignoresSafeArea()
            VStack(spacing: 14) {
                Text(model.confirmNext ? "ВЫ ЕЩЁ СМОТРИТЕ?" : "ДАЛЬШЕ")
                    .font(.system(size: 11, weight: .bold))
                    .tracking(1.3)
                    .foregroundStyle(.white.opacity(0.7))
                if let next = model.nextEpisode {
                    Text(nextLabel(next))
                        .font(.system(size: 17, weight: .semibold))
                        .foregroundStyle(.white)
                    HStack(spacing: 10) {
                        endButton("Смотреть", filled: true) {
                            model.play(next)
                        }
                        if model.confirmNext {
                            endButton("Остаться", filled: false) {
                                model.stay()
                            }
                        }
                    }
                } else {
                    endButton("Смотреть заново", filled: true) {
                        model.seek(0)
                        model.togglePlay()
                    }
                }
            }
        }
    }

    private func nextLabel(_ ep: Episode) -> String {
        if ep.seasonNo > 0 {
            return "Сезон \(ep.seasonNo), эпизод \(ep.episodeNo)"
        }
        return "Эпизод \(ep.episodeNo)"
    }

    private func errorOverlay(_ message: String) -> some View {
        ZStack {
            Color.black.opacity(0.62).ignoresSafeArea()
            VStack(spacing: 14) {
                Text(message).font(.kinoBody).foregroundStyle(.white).multilineTextAlignment(.center)
                HStack(spacing: 10) {
                    endButton("Повторить", filled: true) {
                        Task { await model.issue() }
                    }
                    endButton("Назад", filled: false) { onClose() }
                }
            }
        }
    }

    private func endButton(_ title: String, filled: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(filled ? Color(hex: 0x04210F) : .white)
                .padding(.horizontal, 22)
                .padding(.vertical, 12)
                .background(filled ? kpGreen : Color.white.opacity(0.14), in: Capsule())
        }
        .buttonStyle(.plain)
    }

    private var spinnerOverlay: some View {
        SpinnerView(size: 64)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: Автоскрытие

    private func autoHideLoop() async {
        while !Task.isCancelled {
            try? await Task.sleep(nanoseconds: 500_000_000)
            let hideAfter = model.settings.hideAfter
            guard hideAfter > 0, model.panelVisible, model.menu == nil else { continue }
            guard case .playing = model.phase else { continue }
            if Date().timeIntervalSince(model.lastInput) > Double(hideAfter) {
                withAnimation(.easeInOut(duration: 0.2)) { model.panelVisible = false }
            }
        }
    }
}

// MARK: - Видео-слой

struct VideoLayerView: UIViewRepresentable {
    let player: AVPlayer

    final class Container: UIView {
        override static var layerClass: AnyClass { AVPlayerLayer.self }
        var playerLayer: AVPlayerLayer { layer as! AVPlayerLayer }
    }

    func makeUIView(context: Context) -> Container {
        let v = Container()
        v.playerLayer.player = player
        v.playerLayer.videoGravity = .resizeAspect
        v.backgroundColor = .black
        return v
    }

    func updateUIView(_ uiView: Container, context: Context) {}
}

// MARK: - Громкость

/// Громкость стрима через скрытый MPVolumeView.
final class VolumeController {
    static let shared = VolumeController()
    private var view: MPVolumeView?
    private var base: Float = 0

    /// Начало жеста: громкость отсчитывается от текущей, а не от нуля.
    func begin() {
        if view == nil { attach() }
        base = AVAudioSession.sharedInstance().outputVolume
    }

    func setRelative(_ delta: Double) {
        if view == nil { attach() }
        guard let slider = view?.subviews.compactMap({ $0 as? UISlider }).first else { return }
        let v = min(max(base + Float(delta), 0), 1)
        slider.setValue(v, animated: false)
    }

    private func attach() {
        guard let window = UIApplication.shared.connectedScenes
            .compactMap({ $0 as? UIWindowScene })
            .flatMap(\.windows)
            .first(where: \.isKeyWindow) else { return }
        let v = MPVolumeView(frame: CGRect(x: -100, y: -100, width: 10, height: 10))
        window.addSubview(v)
        view = v
    }
}

/// По содержимому, но не шире `max` (widthIn(max = …) из Compose):
/// .frame(maxWidth:) растягивал короткие пилюли до потолка.
struct CappedWidth: Layout {
    var max: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard let v = subviews.first else { return .zero }
        let ideal = v.sizeThatFits(.unspecified)
        let w = Swift.min(ideal.width, max, proposal.width ?? .infinity)
        return v.sizeThatFits(ProposedViewSize(width: w, height: proposal.height))
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        subviews.first?.place(at: bounds.origin, proposal: ProposedViewSize(width: bounds.width, height: bounds.height))
    }
}
