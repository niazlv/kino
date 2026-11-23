plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.kotlin.android) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.kotlin.serialization) apply false
}

// Сборка идёт вне iCloud-синхронизируемого ~/Documents: там заводились
// дубликаты вида «X 2.dex» прямо в intermediates и слияние dex падало.
// Каталог в ~/Library/Caches iCloud не трогает. Приём из старой оболочки.
allprojects {
    layout.buildDirectory = file("${System.getProperty("user.home")}/Library/Caches/kino-android-native/${project.name}")
}
