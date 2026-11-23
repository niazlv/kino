package ru.sorewa.kino.player

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test

/** Тот же разбор, что provider.VoiceKey на сервере и voiceKey витрины. */
class VoiceTest {

    @Test
    fun kindIsParsedByWords() {
        // «DUB» kinopub и «Дублированный» ЛордФильма — один вид, не две
        // студии. Ключ — как у kinopub: по нему ищется дорожка потока.
        for (s in listOf("DUB", "Дубляж", "Дублированный", "01. Дублированный. DUB (RUS)",
            "01. Дублированный (RUS)", "Полное дублирование (лицензия)", "Дублированный [Лицензия]")) {
            assertEquals(s, "dub", Voice.key(s))
            assertEquals(s, "Дубляж", Voice.title(s))
        }
        assertEquals("mvo", Voice.key("Многоголосый, профессиональный"))
        assertEquals("Многоголосый", Voice.title("02. Многоголосый закадровый. MVO (RUS)"))
        assertEquals("dvo", Voice.key("Двуголосый"))
        assertEquals("avo", Voice.key("Авторский одноголосый"))
        assertEquals("оригинал", Voice.key("Оригинальный"))
        assertEquals("Любительский многоголосый", Voice.title("Многоголосый любительский"))
        assertEquals("Русская дорожка", Voice.title("Track 1 (RUS)"))
        assertEquals("Русская дорожка", Voice.title("Русская дорожка"))
    }

    @Test
    fun foreignWordMakesAStudio() {
        for (s in listOf("AniDUB", "FumoDub", "2D-DUB", "Дубляж Netflix", "Любительский многоголосый")) {
            assertNotEquals(s, Voice.key("DUB"), Voice.key(s))
            assertNotEquals(s, Voice.key("MVO"), Voice.key(s))
        }
        assertEquals("Воротилин", Voice.title("01. Одноголосый. Воротилин (RUS)"))
        assertEquals("агаврилов", Voice.key("А. Гаврилов"))
    }

    @Test
    fun serverKeysWin() {
        // Псевдонимы студий знает только сервер.
        assertEquals("aniliberty", Voice.key("02. AniLiberty (RUS)"))
        Voice.learn(mapOf("02. AniLiberty (RUS)" to ("anilibria" to "AniLiberty")))
        assertEquals("anilibria", Voice.key("02. AniLiberty (RUS)"))
        assertEquals(listOf("x"), Voice.unknown(listOf("02. AniLiberty (RUS)", "x", "x", " ")))
    }
}
