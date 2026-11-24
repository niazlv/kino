package ru.sorewa.kino.ui.theme

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import ru.sorewa.kino.ui.BackMotion

class MotionTest {
    private val w = 1080f
    private val h = 2400f
    private val margin = 21f
    private val shift = 252f

    @Test
    fun `кривая emphasized — непрерывна и от 0 до 1`() {
        assertEquals(0f, EmphasizedPath.transform(0f), 1e-4f)
        assertEquals(1f, EmphasizedPath.transform(1f), 1e-4f)
        assertEquals(0.4f, EmphasizedPath.transform(1f / 6f), 1e-3f)
        var prev = -1f
        for (i in 0..200) {
            val y = EmphasizedPath.transform(i / 200f)
            assertTrue("не убывает", y >= prev - 1e-5f)
            prev = y
        }
    }

    @Test
    fun `сжатая уходящая страница прижата к правому краю с отступом`() {
        val p = BackMotion.closing(1f, w, margin, 0f)
        assertEquals(0.9f, p.scale, 1e-5f)
        val right = w / 2 + p.scale * w / 2 + p.tx
        assertEquals(w - margin, right, 0.5f)
    }

    @Test
    fun `прежняя страница под уходящей — в тени и сдвинута влево`() {
        val p = BackMotion.entering(1f, shift, 0f)
        assertEquals(-shift, p.tx, 1e-3f)
        assertEquals(BackMotion.SCRIM, p.scrim, 1e-5f)
    }

    @Test
    fun `доводка начинается с положения на отпускании и кончается полным экраном`() {
        val s = BackMotion.shrink(0.4f)
        val pre = BackMotion.closing(s, w, margin, 30f)
        val start = BackMotion.closingAfter(s, w, margin, shift, 30f, 0f)
        assertEquals(pre.scale, start.scale, 1e-4f)
        assertEquals(pre.tx, start.tx, 1e-2f)
        assertEquals(1f, start.alpha, 1e-5f)
        // Уходящая гаснет за первую пятую доводки.
        assertEquals(0f, BackMotion.closingAfter(s, w, margin, shift, 30f, 0.2f).alpha, 1e-5f)
        val entEnd = BackMotion.enteringAfter(s, shift, 30f, 1f)
        assertEquals(1f, entEnd.scale, 1e-4f)
        assertEquals(0f, entEnd.tx, 1e-3f)
        assertEquals(0f, entEnd.ty, 1e-3f)
        assertEquals(0f, entEnd.scrim, 1e-5f)
        val entStart = BackMotion.enteringAfter(s, shift, 30f, 0f)
        assertEquals(BackMotion.entering(s, shift, 30f).scale, entStart.scale, 1e-4f)
    }

    @Test
    fun `по вертикали страница идёт за пальцем, но не за край`() {
        val s = BackMotion.shrink(1f)
        val room = (h - h * 0.9f) / 2 - margin
        assertEquals(room, BackMotion.followY(h, h, s, margin), 1e-2f)
        assertEquals(-room, BackMotion.followY(-h, h, s, margin), 1e-2f)
        assertEquals(0f, BackMotion.followY(0f, h, s, margin), 1e-5f)
    }
}
