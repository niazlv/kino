import SwiftUI

/// Хост плеера на весь экран: создаёт модель, стартует, гасит при выходе.
struct PlayerHost: View {
    let app: AppState
    let request: AppState.PlayerRequest

    @State private var model: PlayerModel?

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let model {
                PlayerScreen(model: model) {
                    app.player = nil
                }
            } else {
                SpinnerView(size: 36)
            }
        }
        .onAppear {
            Orientation.lock(.landscape)
            if model == nil {
                switch request {
                case let .stream(episodeId, titleId, autoplay):
                    let m = PlayerModel(episodeId: episodeId, titleId: titleId, autoplay: autoplay)
                    m.app = app
                    model = m
                    m.begin()
                case let .local(entryId):
                    if let entry = LocalLibrary.shared.entry(entryId) {
                        let m = PlayerModel(entry: entry)
                        m.app = app
                        model = m
                        m.begin()
                    }
                }
            }
        }
        .onDisappear {
            model?.stop()
            model = nil
            // Следующая серия пересоздаёт плеер — экран не крутим туда-обратно.
            if app.player == nil {
                Orientation.lock(.allButUpsideDown, rotateTo: .portrait)
            }
        }
    }
}
