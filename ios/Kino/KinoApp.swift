import SwiftUI

@main
struct KinoApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    init() {
        // Bootstrap-вход для прогонов: пара токенов из окружения
        // (SIMCTL_CHILD_KINO_BOOTSTRAP_TOKEN при simctl launch).
        if ProcessInfo.processInfo.environment["KINO_BOOTSTRAP_TOKEN"] != nil {
            LogUI.event("bootstrap token present")
        }
        if let raw = ProcessInfo.processInfo.environment["KINO_BOOTSTRAP_TOKEN"],
           let data = raw.data(using: .utf8),
           let pair = try? JSONDecoder().decode(TokenPair.self, from: data) {
            TokenStore.save(pair)
            Api.shared.saveSession(pair)
        }
    }

    var body: some Scene {
        WindowGroup {
            RootView()
        }
    }
}

/// Фоновая сессия загрузок: система будит приложение, когда задачи кончились
/// в фоне, — сессию надо поднять тем же идентификатором и вернуть обработчик.
final class AppDelegate: NSObject, UIApplicationDelegate {
    /// Допустимые ориентации: плеер держит альбомную (как sensorLandscape
    /// у PlayerActivity на Android), остальное — как в Info.plist.
    static var orientationLock: UIInterfaceOrientationMask = .allButUpsideDown

    func application(_ application: UIApplication, supportedInterfaceOrientationsFor window: UIWindow?) -> UIInterfaceOrientationMask {
        AppDelegate.orientationLock
    }

    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        _ = DownloadManager.shared
        return true
    }

    func application(_ application: UIApplication, handleEventsForBackgroundURLSession identifier: String, completionHandler: @escaping () -> Void) {
        guard identifier == DownloadManager.sessionId else { completionHandler(); return }
        DownloadManager.shared.backgroundCompletion = completionHandler
    }
}

/// Поворот экрана под плеер и обратно.
@MainActor
enum Orientation {
    static func lock(_ mask: UIInterfaceOrientationMask, rotateTo: UIInterfaceOrientationMask? = nil) {
        AppDelegate.orientationLock = mask
        for case let scene as UIWindowScene in UIApplication.shared.connectedScenes {
            scene.requestGeometryUpdate(.iOS(interfaceOrientations: rotateTo ?? mask)) { _ in }
            scene.windows.first(where: \.isKeyWindow)?.rootViewController?.setNeedsUpdateOfSupportedInterfaceOrientations()
        }
    }
}
