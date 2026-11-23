package ru.sorewa.kino.ui.player

import androidx.compose.ui.graphics.vector.PathNode
import androidx.compose.ui.graphics.vector.PathParser
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class AbsolutePathTest {
    private fun abs(d: String) = absolutePath(PathParser().parsePathString(d).toNodes())

    @Test fun `m после z отсчитывается от начала контура`() {
        // Пауза: вторая полоска должна начаться в (14,5), а не в (14,19).
        val n = abs("M6 5h4v14H6zm8 0h4v14h-4z")
        assertEquals(PathNode.MoveTo(14f, 5f), n[5])
        assertEquals(PathNode.HorizontalTo(18f), n[6])
        assertEquals(PathNode.VerticalTo(19f), n[7])
        assertEquals(PathNode.HorizontalTo(14f), n[8])
    }

    @Test fun `информация - точка, палочка и кружок на местах`() {
        val moves = abs("M11 7h2v2h-2zm0 4h2v6h-2zm1-9C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2z")
            .filterIsInstance<PathNode.MoveTo>()
        assertEquals(listOf(PathNode.MoveTo(11f, 7f), PathNode.MoveTo(11f, 11f), PathNode.MoveTo(12f, 2f)), moves)
    }

    @Test fun `относительных узлов не остаётся`() {
        val n = abs("M12 5V1l5 5-5 5V7a6 6 0 1 0 6 6h2a8 8 0 1 1-8-8z")
        assertTrue(n.none { it::class.simpleName!!.startsWith("Relative") })
        assertEquals(PathNode.ArcTo(6f, 6f, 0f, true, false, 18f, 13f), n[5])
    }
}
