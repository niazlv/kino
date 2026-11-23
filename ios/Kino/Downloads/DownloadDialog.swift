import SwiftUI

/// Диалог «Скачать» — 1:1 с DownloadDialog.kt, адаптированный под нативную
/// качалку: файл для офлайна (.movpkg) вместо .mkv. Как и в MKV на Android,
/// в файл ложатся все озвучки потока; субтитры — только нужных языков
/// (TrackPlan.subtitleLanguages: у kinopub их до сорока). Выбранная студия решает
/// площадку (у kinopub все студии — дорожки одного потока, у остальных —
/// отдельные потоки) и включается в плеере первой.
struct DownloadDialog: View {
    let app: AppState
    let detail: TitleDetail
    /// Серия, с которой открыли (nil — сезон/весь тайтл).
    let episode: Episode?
    let seasonNo: Int
    /// С чего начать: «Скачать сезон» / «Все серии» из меню сезона.
    var initialScope: Scope? = nil

    @Environment(\.dismiss) private var dismiss
    @State private var scope: Scope = .episode
    @State private var picked: Set<Int64> = []
    @State private var voice: VoiceOption?
    @State private var voices: [VoiceOption] = []
    @State private var error: String?
    @State private var busy = false

    enum Scope: Equatable {
        case episode, season(Int), all, pick
    }

    private var series: Bool { !isMovie(detail) }
    private var seasons: [Int] { seasonsOf(detail) }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Скачать").font(.kinoTitle).foregroundStyle(Pal.textMain)
                Text(splitTitle(detail.title.title).0).font(.kinoBodyMedium).foregroundStyle(Pal.textDim)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    if series {
                        Text("ЧТО СКАЧАТЬ").overlineStyle()
                        scopePills
                        if scope == .pick {
                            episodePicker
                        }
                    }
                    Text("ОЗВУЧКА").overlineStyle()
                    voicePills
                    Text("КАЧЕСТВО").overlineStyle()
                    Text("Лучшее — поток целиком, как отдаёт площадка")
                        .font(.kinoBodySmall)
                        .foregroundStyle(Pal.textFaint)
                    if let error {
                        Text(error).font(.kinoBodySmall).foregroundStyle(Pal.danger)
                    }
                }
            }
            PrimaryButton(title: countLabel.isEmpty ? "Скачать" : "Скачать · \(countLabel)", busy: busy) {
                Task { await start() }
            }
        }
        .padding(20)
        .presentationDetents([.large])
        .presentationBackground(Pal.surface1)
        .task { await loadVoices() }
        .onAppear {
            // Без серии «Эту серию» нечего качать — начать с сезона/всего.
            scope = initialScope ?? (episode != nil || !series ? .episode : (seasons.count == 1 ? .all : .season(seasonNo)))
        }
    }

    private var scopePills: some View {
        FlowLayout(spacing: 8) {
            if episode != nil {
                Pill(title: "Эту серию", selected: scope == .episode) { scope = .episode }
            }
            if seasons.count == 1 {
                Pill(title: "Все серии", selected: scope == .all) { scope = .all }
            } else {
                Pill(title: "Сезон \(seasonNo)", selected: scope == .season(seasonNo)) { scope = .season(seasonNo) }
                if seasons.count > 1 {
                    Pill(title: "Весь сериал", selected: scope == .all) { scope = .all }
                }
            }
            Pill(title: "Выбрать серии", selected: scope == .pick) { scope = .pick }
        }
    }

    /// Пикер серий: группы по сезонам с чекбоксами.
    /// Список серий прокручивается в своей рамке: без ScrollView высота
    /// сверх рамки (45 серий — ~1300 pt) вылезала поверх остальной шторки.
    /// Высота явная — внутри внешнего ScrollView maxHeight не ограничивает.
    private var episodePicker: some View {
        let rows = seasons.reduce(0) { $0 + 1 + episodesOf(detail, $1).count }
        return ScrollView {
            episodeRows
        }
        .frame(height: min(CGFloat(rows) * 34, 360))
    }

    private var episodeRows: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(seasons, id: \.self) { s in
                let eps = episodesOf(detail, s)
                let done = eps.filter { picked.contains($0.id) }.count
                Button {
                    if done == eps.count {
                        eps.forEach { picked.remove($0.id) }
                    } else {
                        eps.forEach { picked.insert($0.id) }
                    }
                } label: {
                    HStack(spacing: 10) {
                        checkmark(checked: done == eps.count)
                        Text("Сезон \(s) (\(done)/\(eps.count))")
                            .font(.kinoBody)
                            .foregroundStyle(Pal.textMain)
                        Spacer()
                    }
                    .padding(.vertical, 8)
                }
                .buttonStyle(.plain)
                ForEach(eps) { ep in
                    Button {
                        if picked.contains(ep.id) { picked.remove(ep.id) } else { picked.insert(ep.id) }
                    } label: {
                        HStack(spacing: 10) {
                            checkmark(checked: picked.contains(ep.id))
                                .padding(.leading, 24)
                            Text("\(ep.episodeNo) серия\(ep.title.map { " · \($0)" } ?? "")")
                                .font(.kinoBodySmall)
                                .foregroundStyle(ep.status == "completed" ? Pal.textFaint : Pal.textMain)
                                .lineLimit(1)
                            Spacer()
                        }
                        .padding(.vertical, 6)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    private func checkmark(checked: Bool) -> some View {
        ZStack {
            RoundedRectangle(cornerRadius: 5)
                .stroke(checked ? Pal.accent : Pal.textFaint, lineWidth: 1.5)
                .frame(width: 20, height: 20)
                .background(checked ? Pal.accent.opacity(0.2) : .clear, in: RoundedRectangle(cornerRadius: 5))
            if checked {
                Image(systemName: "checkmark").font(.system(size: 11, weight: .bold)).foregroundStyle(Pal.accent)
            }
        }
    }

    private var voicePills: some View {
        Group {
            if voices.isEmpty {
                Text("Озвучек у серии не найдено — скачается как есть")
                    .font(.kinoBodySmall).foregroundStyle(Pal.textFaint)
            } else {
                FlowLayout(spacing: 8) {
                    Pill(title: "По умолчанию", selected: voice == nil) { voice = nil }
                    ForEach(voices) { v in
                        Pill(title: v.title, selected: voice?.key == v.key) { voice = v }
                    }
                }
                Text("В файл попадут все озвучки потока и субтитры на русском, украинском, английском и языке оригинала — переключаются в плеере и без сети. Выбранная озвучка включится первой.")
                    .font(.kinoBodySmall).foregroundStyle(Pal.textFaint)
            }
        }
    }

    private var countLabel: String {
        let n = pickedEpisodes().count
        if n == 1 { return "" }
        return "\(n) \(plural(n, "серия", "серии", "серий"))"
    }

    private func pickedEpisodes() -> [Episode] {
        switch scope {
        case .episode:
            if let episode { return [episode] }
            return series ? [] : Array(detail.episodes.prefix(1))
        case .season(let s):
            return episodesOf(detail, s)
        case .all:
            return detail.episodes
        case .pick:
            return detail.episodes.filter { picked.contains($0.id) }
        }
    }

    private func loadVoices() async {
        guard let ep = episode ?? detail.episodes.first else { return }
        if let list = try? await Api.shared.voices(ep.id) {
            voices = list.voices
        }
    }

    private func start() async {
        let eps = pickedEpisodes()
        guard !eps.isEmpty else {
            error = "Ничего не выбрано"
            return
        }
        busy = true
        for ep in eps {
            let entryId = LocalLibrary.entryId(titleId: detail.title.id, season: ep.seasonNo, episode: ep.episodeNo)
            // «Фильм» — по виду тайтла: у сериала бывает сезон 0 (допматериалы).
            let label = series ? "\(ep.seasonNo) сезон, \(ep.episodeNo) серия" : "Фильм"
            await DownloadManager.shared.enqueue(
                entryId: entryId, title: splitTitle(detail.title.title).0,
                episodeLabel: label, episodeId: ep.id, voice: voice
            )
        }
        busy = false
        dismiss()
    }
}
