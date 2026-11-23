// Нативное приложение (этап 1): телефон + Android TV.
// Живёт отдельно от Capacitor-оболочки web/apps/android — тот остаётся
// до публикации ядра, потом убирается.
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "kino-native"
include(":app")
