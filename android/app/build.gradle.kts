import java.util.Properties

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

// Ключ подписи — тот же, что у Capacitor-оболочки: APK встаёт поверх
// установленного приложения как обновление, а не вторая копия.
val keystoreProps = Properties()
val keystoreFile = rootProject.file("../web/apps/android/keystore/keystore.properties")
if (keystoreFile.exists()) keystoreFile.inputStream().use { keystoreProps.load(it) }

// Адрес сервера по умолчанию приходит при сборке, в исходниках его нет:
// -PkinoUrl=… (или kinoUrl=… в ~/.gradle/gradle.properties), иначе KINO_URL из
// окружения. publish-apk.sh передаёт https://$DOMAIN из deploy/prod/local.env.
// Это только стартовая точка и источник обновлений (ADR-0016): человек может
// указать свой сервер в приложении (вход → «Сменить», профиль → «Сервер»).
// Пусто (-PkinoUrl=) — при первом запуске приложение спросит адрес, а
// обновления такой сборки ставятся вручную.
// Отладка против локального сервера: ./gradlew -PkinoUrl=http://10.0.2.2:8080 :app:assembleDebug
val kinoUrl = (findProperty("kinoUrl") as String?) ?: System.getenv("KINO_URL") ?: ""

android {
    namespace = "ru.sorewa.kino"
    compileSdk = 36

    defaultConfig {
        applicationId = "ru.sorewa.kino"
        minSdk = 24
        targetSdk = 36
        versionCode = 27
        versionName = "0.2.17"
        buildConfigField("String", "BASE_URL", "\"$kinoUrl\"")
        // Копия рядом с настоящим приложением (проверка на телефоне, не затирая
        // установленное): ./gradlew -PappIdSuffix=.check :app:assembleDebug
        (findProperty("appIdSuffix") as String?)?.let { applicationIdSuffix = it }
    }

    signingConfigs {
        create("release") {
            if (keystoreFile.exists()) {
                // storeFile в properties указан относительно каталога старой
                // оболочки (web/apps/android/android) — резолвим от корня репо.
                val storePath = keystoreProps.getProperty("storeFile")
                val resolved = rootProject.file("../web/apps/android/android/$storePath")
                storeFile = if (resolved.exists()) resolved else rootProject.file(storePath)
                storePassword = keystoreProps.getProperty("storePassword")
                keyAlias = keystoreProps.getProperty("keyAlias")
                keyPassword = keystoreProps.getProperty("keyPassword")
            }
        }
    }

    buildTypes {
        debug {
            // Локальный стенд живёт по http; кого пускать по http, решает приложение (см. network_security_config.xml).
            isDefault = true
            manifestPlaceholders["usesCleartextTraffic"] = "true"
        }
        release {
            // R8 вырезает неиспользуемый код (прежде всего тысячи значков
            // material-icons-extended), но классы не переименовывает
            // (-dontobfuscate в proguard-rules.pro): трассы падений читаются
            // без mapping.txt, поиск классов по имени не ломается.
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            // Открытый HTTP решает network_security_config.xml (он главнее этого
            // флага с API 24): свой сервер в домашней сети бывает и по http,
            // а в интернет приложение по http не пускает (ServerAddress.allowsCleartext).
            manifestPlaceholders["usesCleartextTraffic"] = "true"
            if (keystoreFile.exists()) signingConfig = signingConfigs.getByName("release")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_21
        targetCompatibility = JavaVersion.VERSION_21
    }
    kotlin {
        compilerOptions {
            jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_21
        }
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }

    packaging {
        // Утилита kinopub и ffmpeg лежат в APK как «библиотеки» (единственный
        // способ запускать исполняемые файлы на Android) и должны быть
        // распакованы на диск при установке — иначе их нельзя выполнить.
        jniLibs { useLegacyPackaging = true }
    }
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.datastore.preferences)

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.graphics)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons)

    implementation(libs.media3.exoplayer)
    implementation(libs.media3.exoplayer.hls)
    implementation(libs.media3.ui)
    implementation(libs.media3.session)

    implementation(libs.okhttp)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.coil.compose)
    implementation(libs.zxing.core)
    implementation(libs.androidx.documentfile)
    implementation(libs.androidx.browser)
    // Главный экран Android TV: «Продолжить просмотр» и канал «Новые серии».
    implementation(libs.androidx.tvprovider)
    implementation(libs.androidx.work.runtime)

    testImplementation(libs.junit4)
}
