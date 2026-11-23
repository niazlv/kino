import SwiftUI

// Общие компоненты — зеркало ui/common/Components.kt (телефонная ветка).

// MARK: - Арты

/// Детерминированный градиент из хеша имени + название по центру.
struct ArtPlaceholder: View {
    let name: String
    var showName = true

    var body: some View {
        let h = javaHash(name)
        let hue = Double((h & 0x7FFF_FFFF) % 360)
        let c1 = Color(hue: hue / 360, saturation: 0.35, brightness: 0.28)
        let c2 = Color(hue: (hue + 40).truncatingRemainder(dividingBy: 360) / 360, saturation: 0.45, brightness: 0.14)
        ZStack {
            LinearGradient(colors: [c1, c2], startPoint: .leading, endPoint: .trailing)
            if showName {
                Text(splitTitle(name).0)
                    .font(.kinoTitleSmall)
                    .foregroundStyle(.white.opacity(0.85))
                    .multilineTextAlignment(.center)
                    .lineLimit(4)
                    .padding(10)
            }
        }
    }

    /// Java String.hashCode — чтобы градиенты совпадали с Android.
    private func javaHash(_ s: String) -> Int32 {
        var h: Int32 = 0
        for ch in s.unicodeScalars {
            h = h &* 31 &+ Int32(truncatingIfNeeded: ch.value)
        }
        return h
    }
}

/// Постер тайтла: арта нет/не загрузилась → плейсхолдер.
struct PosterImage: View {
    let titleId: Int64
    let hasArt: Bool
    let name: String

    @State private var img: UIImage?
    /// Арт не пришёл — заглушка с названием, а не вечное мерцание.
    @State private var failed = false

    var body: some View {
        ZStack {
            if let img {
                Image(uiImage: img).resizable().scaledToFill()
            } else if hasArt && !failed {
                Shimmer()
            } else {
                ArtPlaceholder(name: name)
            }
        }
        .task(id: titleId) {
            guard hasArt else { return }
            if let hit = ArtStore.shared.cached("api/v1/titles/\(titleId)/art") { img = hit; return }
            img = await ArtStore.shared.titleArt(titleId)
            if img == nil, !Task.isCancelled { failed = true }
        }
    }
}

/// Кадр серии: кадр → постер тайтла → плейсхолдер.
struct ShotImage: View {
    let episodeId: Int64
    var fallbackTitleId: Int64? = nil
    var fallbackHasArt = false
    let name: String

    @State private var img: UIImage?
    @State private var failed = false

    var body: some View {
        ZStack {
            if let img {
                Image(uiImage: img).resizable().scaledToFill()
            } else if failed {
                ArtPlaceholder(name: name, showName: false)
            } else {
                Shimmer()
            }
        }
        // Кадр → постер тайтла → заглушка (Components.kt).
        .task(id: episodeId) {
            if let hit = ArtStore.shared.cached("api/v1/episodes/\(episodeId)/art") { img = hit; return }
            if let got = await ArtStore.shared.episodeArt(episodeId) { img = got; return }
            if let tid = fallbackTitleId, fallbackHasArt {
                img = await ArtStore.shared.titleArt(tid)
            }
            if img == nil, !Task.isCancelled { failed = true }
        }
    }
}

struct Shimmer: View {
    @State private var on = false

    var body: some View {
        Pal.surface2
            .opacity(on ? 0.9 : 0.45)
            .onAppear {
                withAnimation(.easeInOut(duration: 0.9).repeatForever(autoreverses: true)) { on = true }
            }
    }
}

// MARK: - Мелочи

struct ProgressLine: View {
    let frac: Double
    var height: CGFloat = 3

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule().fill(Color.white.opacity(0.25))
                Capsule()
                    .fill(Pal.accent)
                    .frame(width: max(geo.size.width * (frac.isFinite ? min(max(frac, 0), 1) : 0), geo.size.width * 0.02))
            }
        }
        .frame(height: height)
    }
}

struct Badge: View {
    let text: String

    var body: some View {
        Text(text)
            .font(.system(size: 11, weight: .bold))
            .foregroundStyle(Pal.onAccent)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(Pal.accent, in: RoundedRectangle(cornerRadius: Pal.rXs))
    }
}

struct Avatar: View {
    let name: String
    var size: CGFloat = 32

    var body: some View {
        ZStack {
            Circle().fill(
                LinearGradient(colors: [Pal.accent, Pal.accentAlt], startPoint: .topLeading, endPoint: .bottomTrailing)
            )
            Text(String(name.prefix(1)).uppercased())
                .font(.system(size: size * 0.42, weight: .bold))
                .foregroundStyle(Pal.onAccent)
        }
        .frame(width: size, height: size)
    }
}

/// Логотип: зелёная плитка с билетом + «kino».
struct BrandMark: View {
    var tile: CGFloat = 32
    var icon: CGFloat = 18
    var showWord = true
    var wordFont: Font = .kinoHeadline

    var body: some View {
        HStack(spacing: 8) {
            ZStack {
                RoundedRectangle(cornerRadius: tile * 0.28).fill(Pal.accent)
                Image(systemName: "ticket.fill")
                    .font(.system(size: icon))
                    .foregroundStyle(Pal.onAccent)
            }
            .frame(width: tile, height: tile)
            if showWord {
                Text("kino").font(wordFont).foregroundStyle(Pal.textMain)
            }
        }
    }
}

// MARK: - Кнопки

struct PrimaryButton: View {
    let title: String
    var icon: String? = nil
    var busy = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                if busy {
                    SpinnerView(size: 20)
                } else if let icon {
                    Image(systemName: icon).font(.system(size: 18))
                }
                Text(title).font(.system(size: 16, weight: .medium))
            }
            .foregroundStyle(Pal.onAccent)
            .frame(maxWidth: .infinity)
            .frame(height: 52)
            .background(Pal.accent, in: RoundedRectangle(cornerRadius: Pal.rM))
        }
        .buttonStyle(.plain)
        .disabled(busy)
    }
}

struct SecondaryButton: View {
    let title: String
    var icon: String? = nil
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                if let icon { Image(systemName: icon).font(.system(size: 18)) }
                Text(title).font(.system(size: 16, weight: .medium))
            }
            .foregroundStyle(Pal.textMain)
            .frame(maxWidth: .infinity)
            .frame(height: 52)
            .background(Pal.surface3, in: RoundedRectangle(cornerRadius: Pal.rM))
        }
        .buttonStyle(.plain)
    }
}

/// Спиннер: белая дуга на кольце.
struct SpinnerView: View {
    var size: CGFloat = 36
    var ring: Color = Color.white.opacity(0.25)

    var body: some View {
        Circle()
            .stroke(ring, lineWidth: 3)
            .overlay(
                Circle()
                    .trim(from: 0, to: 0.25)
                    .stroke(Color.white, style: StrokeStyle(lineWidth: 3, lineCap: .round))
                    .rotationEffect(.degrees(rotation))
            )
            .frame(width: size, height: size)
            .onAppear {
                withAnimation(.linear(duration: 0.9).repeatForever(autoreverses: false)) { rotation = 360 }
            }
    }

    @State private var rotation: Double = 0
}

// MARK: - Состояния

struct EmptyState: View {
    let icon: String
    let title: String
    var text: String = ""
    var actionTitle: String? = nil
    var action: (() -> Void)? = nil

    var body: some View {
        VStack(spacing: 12) {
            ZStack {
                Circle().fill(Pal.surface2).frame(width: 72, height: 72)
                Image(systemName: icon).font(.system(size: 30)).foregroundStyle(Pal.textDim)
            }
            Text(title).font(.kinoTitleMedium).foregroundStyle(Pal.textMain)
            if !text.isEmpty {
                Text(text).font(.kinoBodyMedium).foregroundStyle(Pal.textDim).multilineTextAlignment(.center)
            }
            if let actionTitle, let action {
                SecondaryButton(title: actionTitle, action: action)
                    .frame(maxWidth: 240)
                    .padding(.top, 4)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 40)
    }
}

struct ErrorView: View {
    var text: String = ""
    let retry: () -> Void

    var body: some View {
        EmptyState(icon: "arrow.clockwise", title: "Не получилось загрузить", text: text, actionTitle: "Повторить", action: retry)
    }
}

struct LoadMore: View {
    var body: some View {
        HStack {
            Spacer()
            SpinnerView(size: 28).padding(.vertical, 16)
            Spacer()
        }
    }
}

// MARK: - Каркасные элементы

struct Panel<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) { content }
            .padding(16)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Pal.surface1, in: RoundedRectangle(cornerRadius: 18))
            .overlay(RoundedRectangle(cornerRadius: 18).stroke(Pal.stroke))
    }
}

struct SectionHeader: View {
    let title: String
    var count: Int? = nil
    var actionTitle: String? = nil
    var action: (() -> Void)? = nil

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(title).font(.kinoTitleMedium).foregroundStyle(Pal.textMain)
            if let count {
                Text(String(count)).font(.kinoBodyMedium).foregroundStyle(Pal.textFaint)
            }
            Spacer()
            if let actionTitle, let action {
                Button(actionTitle, action: action)
                    .font(.kinoLabel)
                    .foregroundStyle(Pal.accent)
                    .buttonStyle(.plain)
            }
        }
    }
}

struct ScreenHeader: View {
    let title: String
    var subtitle: String? = nil
    var actions: (() -> AnyView)? = nil

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.kinoHeadline).foregroundStyle(Pal.textMain)
                if let subtitle {
                    Text(subtitle).font(.kinoBodyMedium).foregroundStyle(Pal.textDim)
                }
            }
            Spacer()
            if let actions { actions().anyView }
        }
    }
}

// MARK: - Пилюли и действия

struct Pill: View {
    let title: String
    var icon: String? = nil
    var trailing: (() -> AnyView)? = nil
    var selected = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                if let icon { Image(systemName: icon).font(.system(size: 14)) }
                Text(title).font(.kinoBodyMedium)
                if let trailing { trailing().anyView }
            }
            .foregroundStyle(selected ? Pal.onAccent : Pal.textMain)
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
            .background(
                Capsule().fill(selected ? Pal.accent : Pal.surface2)
            )
            .overlay(Capsule().stroke(selected ? Color.clear : Pal.stroke))
        }
        .buttonStyle(.plain)
    }
}

/// Горизонтальный ряд пилюль.
struct PillRow: View {
    let items: [PillItem]

    struct PillItem: Identifiable {
        let id: String
        let title: String
        var icon: String? = nil
        var selected = false
        var action: () -> Void
    }

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(items) { item in
                    Pill(title: item.title, icon: item.icon, selected: item.selected, action: item.action)
                }
            }
            .padding(.horizontal, 20)
        }
    }
}

struct IconAction: View {
    let icon: String
    let label: String
    var active = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 5) {
                Image(systemName: icon).font(.system(size: 22))
                Text(label).font(.system(size: 12, weight: .medium))
            }
            .foregroundStyle(active ? Pal.accent : Pal.textDim)
            .frame(maxWidth: .infinity)
        }
        .buttonStyle(.plain)
    }
}

// MARK: - Карточки

/// Постер 2:3 с названием и подписью снизу.
struct PosterCard: View {
    let titleId: Int64
    let hasArt: Bool
    let name: String
    var sub: String = ""
    var badge: String? = nil
    var progress: Double? = nil
    var dim = false
    var width: CGFloat? = nil
    var onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            VStack(alignment: .leading, spacing: 6) {
                // Постер 2:3 по ширине карточки: Color задаёт рамку, картинка
                // заполняет её и обрезается, не растягивая ряд.
                Color.clear
                    .aspectRatio(2 / 3, contentMode: .fit)
                    .overlay { PosterImage(titleId: titleId, hasArt: hasArt, name: name) }
                    .clipShape(RoundedRectangle(cornerRadius: 12))
                    .overlay(alignment: .topLeading) {
                        if let badge { Badge(text: badge).padding(6) }
                    }
                    .overlay {
                        if dim { Color.black.opacity(0.4) }
                    }
                    .overlay(alignment: .bottom) {
                        if let progress {
                            ProgressLine(frac: progress, height: 3)
                                .padding(.horizontal, 0)
                                .padding(.bottom, 0)
                                .background(Color.black.opacity(0.55))
                        }
                    }
                Text(name)
                    .font(.kinoBodyMedium.weight(.medium))
                    .foregroundStyle(Pal.textMain)
                    .lineLimit(2)
                if !sub.isEmpty {
                    Text(sub).font(.kinoBodySmall).foregroundStyle(Pal.textDim).lineLimit(1)
                }
            }
            // Ширина — всей карточке: иначе длинное название раздвигает ряд.
            .frame(width: width, alignment: .leading)
            .frame(maxWidth: width == nil ? .infinity : nil, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

/// Пункт контекстного меню карточки (долгое нажатие — как onLongClick на Android).
struct CardAction: Identifiable {
    var id: String { title }
    let title: String
    let icon: String
    var destructive = false
    let action: () -> Void
}

extension View {
    /// Контекстное меню из пунктов; пустой список — меню нет вовсе.
    @ViewBuilder
    func cardMenu(_ actions: [CardAction]) -> some View {
        if actions.isEmpty {
            self
        } else {
            contextMenu {
                ForEach(actions) { a in
                    Button(role: a.destructive ? .destructive : nil, action: a.action) {
                        Label(a.title, systemImage: a.icon)
                    }
                }
            }
        }
    }
}

/// Тайтл-карточка каталога/поиска: постер + вид · год.
struct TitleCard: View {
    let title: Title
    var badge: String? = nil
    var onTap: () -> Void

    var body: some View {
        PosterCard(
            titleId: title.id, hasArt: title.hasArt, name: title.title,
            sub: kindSubtitle(title.kind, title.year, title.species),
            badge: badge, onTap: onTap
        )
    }
}

/// Широкая карточка «Продолжить смотреть»: кадр 16:9 с градиентом.
struct WideCard: View {
    let item: ContinueItem
    var actions: [CardAction] = []
    var onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            VStack(alignment: .leading, spacing: 6) {
                Color.clear
                    .frame(width: 264, height: 264 * 9 / 16)
                    .overlay {
                        ShotImage(episodeId: item.episodeId, fallbackTitleId: item.titleId, fallbackHasArt: item.hasArt, name: item.title)
                    }
                    .clipShape(RoundedRectangle(cornerRadius: 14))
                    .overlay(
                        LinearGradient(colors: [.clear, .black.opacity(0.9)], startPoint: .center, endPoint: .bottom)
                    )
                    .overlay(alignment: .center) {
                        ZStack {
                            Circle().fill(Color.black.opacity(0.4)).frame(width: 44, height: 44)
                                .overlay(Circle().stroke(Color.white.opacity(0.33)))
                            Image(systemName: "play.fill").font(.system(size: 17)).foregroundStyle(.white)
                        }
                    }
                    .overlay(alignment: .bottomLeading) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(item.title).font(.kinoTitleSmall).foregroundStyle(.white).lineLimit(1)
                            Text(describeContinue(item)).font(.kinoBodySmall).foregroundStyle(.white.opacity(0.8)).lineLimit(1)
                            ProgressLine(frac: item.frac).padding(.top, 2)
                        }
                        .padding(10)
                    }
            }
        }
        .buttonStyle(.plain)
        .cardMenu(actions)
    }
}

extension ContinueItem {
    var frac: Double {
        guard durationMs > 0 else { return 0 }
        return Double(positionMs) / Double(durationMs)
    }
}

// MARK: - Прочее

extension AnyView { var anyView: AnyView { self } }

/// Горизонтальная рельса карточек.
struct Rail<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(alignment: .top, spacing: 12) { content }
                .padding(.horizontal, 20)
                .padding(.vertical, 4)
        }
    }
}

/// Скелет постерной сетки 3×2.
struct PosterGridSkeleton: View {
    var body: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 104), spacing: 12)], spacing: 16) {
            ForEach(0..<9, id: \.self) { _ in
                VStack(alignment: .leading, spacing: 6) {
                    Shimmer().aspectRatio(2 / 3, contentMode: .fit).clipShape(RoundedRectangle(cornerRadius: 12))
                    Shimmer().frame(height: 12).clipShape(Capsule())
                }
            }
        }
    }
}

/// Скелет героя «Продолжить».
struct HeroSkeleton: View {
    var body: some View {
        VStack(spacing: 12) {
            Shimmer().aspectRatio(16 / 10, contentMode: .fit).clipShape(RoundedRectangle(cornerRadius: 22))
            Shimmer().frame(height: 52).clipShape(RoundedRectangle(cornerRadius: 14))
            HStack(spacing: 12) {
                ForEach(0..<3, id: \.self) { _ in
                    Shimmer().aspectRatio(2 / 3, contentMode: .fit).clipShape(RoundedRectangle(cornerRadius: 12))
                }
            }
        }
    }
}
