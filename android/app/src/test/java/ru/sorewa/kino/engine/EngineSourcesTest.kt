package ru.sorewa.kino.engine

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import ru.sorewa.kino.data.EngineSourceInfo
import ru.sorewa.kino.data.EngineSourceSetting

class EngineSourcesTest {

    @Test
    fun engineAddsNewSourcesAfterBundledOnes() {
        val engine = EngineSources.fromEngine(
            listOf(
                EngineSourceInfo(id = "anilibria", name = "Анилибрия", origin = "builtin"),
                EngineSourceInfo(id = "animevost", name = "AnimeVost", origin = "bundled", version = "1.0.1", note = "Свои MP4"),
                EngineSourceInfo(id = "jutsu_ru", name = "Закрыта", hidden = true),
                EngineSourceInfo(id = "broken", broken = "нет name"),
            ),
        )
        val all = EngineSources.merge(EngineSources.bundled, engine)
        assertEquals(EngineSources.bundled.map { it.id }, all.take(EngineSources.bundled.size).map { it.id })
        val vost = all.last()
        assertEquals("animevost", vost.id)
        assertEquals("bundled", vost.origin)
        assertEquals("1.0.1", vost.version)
        // Скрытые и сломанные — не для выбора.
        assertNull(all.firstOrNull { it.id == "jutsu_ru" || it.id == "broken" })
    }

    @Test
    fun localScriptReplacingBuiltinKeepsKeyField() {
        val engine = EngineSources.fromEngine(
            listOf(EngineSourceInfo(id = "filmix", name = "Filmix (правка)", origin = "local", version = "2.0.0", note = "правка")),
        )
        val filmix = EngineSources.merge(EngineSources.bundled, engine).first { it.id == "filmix" }
        assertEquals("local", filmix.origin)
        assertEquals("Filmix (правка)", filmix.title)
        assertEquals("FILMIX_TOKEN", filmix.keyEnv) // движок о ключе промолчал — поле остаётся
    }

    @Test
    fun mainKeyIsFirstSecretNonAdvancedSetting() {
        val s = EngineSources.fromEngine(
            listOf(
                EngineSourceInfo(
                    id = "site", name = "Сайт", origin = "local",
                    settings = listOf(
                        EngineSourceSetting(env = "SITE_API", label = "Адрес"),
                        EngineSourceSetting(env = "SITE_REFRESH", label = "Refresh", secret = true, advanced = true),
                        EngineSourceSetting(env = "SITE_TOKEN", label = "Токен", secret = true),
                    ),
                ),
            ),
        ).single()
        assertEquals("SITE_TOKEN", s.keyEnv)
        assertEquals("Токен", s.keyLabel)
    }
}
