package ru.sorewa.kino.ui

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.google.zxing.BarcodeFormat
import com.google.zxing.qrcode.QRCodeWriter
import ru.sorewa.kino.KinoApp
import ru.sorewa.kino.data.DeviceBegin
import ru.sorewa.kino.data.describeApiError
import ru.sorewa.kino.ui.theme.Accent
import ru.sorewa.kino.ui.theme.Surface2
import ru.sorewa.kino.ui.theme.TextDim
import ru.sorewa.kino.ui.theme.TextMain

/**
 * Вход кодом (device-flow): устройство просит код у сервера и ждёт, пока
 * его одобрят там, где человек уже вошёл — в приложении на телефоне
 * («Подключить телевизор») или на сайте по QR. Код истёк — берём новый.
 */
@Composable
fun DeviceCodeLogin(
    onDone: () -> Unit,
    big: Boolean,
    onOffline: (() -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    val container = remember { KinoApp.instance.container }
    var begin by remember { mutableStateOf<DeviceBegin?>(null) }
    var error by remember { mutableStateOf("") }
    var attempt by remember { mutableIntStateOf(0) }

    LaunchedEffect(attempt) {
        begin = null
        error = ""
        val b = try {
            container.auth.beginDeviceLogin()
        } catch (e: Exception) {
            error = describeApiError(e)
            return@LaunchedEffect
        }
        begin = b
        val ok = try {
            container.auth.awaitDeviceApproval(b)
        } catch (e: Exception) {
            error = describeApiError(e)
            return@LaunchedEffect
        }
        if (ok) onDone()
        else attempt++ // истёк или отклонён — новый код без лишних кнопок
    }

    val b = begin
    Column(modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        when {
            error.isNotEmpty() -> {
                Text(error, color = MaterialTheme.colorScheme.error, textAlign = TextAlign.Center)
                Spacer(Modifier.height(16.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    SecondaryButton("Повторить") { attempt++ }
                    if (onOffline != null && (error.startsWith("нет связи") || !ru.sorewa.kino.util.isOnline())) {
                        PrimaryButton("Смотреть скачанное", onClick = onOffline)
                    }
                }
            }
            b == null -> CircularProgressIndicator(color = Accent)
            else -> Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Text("Код для входа", style = MaterialTheme.typography.titleMedium, color = TextDim)
                Spacer(Modifier.height(8.dp))
                // Код крупно — с дивана читается без усилий.
                Text(
                    b.userCode,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.Bold,
                    fontSize = if (big) 46.sp else 34.sp,
                    letterSpacing = 2.sp,
                    softWrap = false,
                    color = Accent,
                    maxLines = 1,
                    modifier = Modifier
                        .background(Surface2, RoundedCornerShape(16.dp))
                        .padding(horizontal = 22.dp, vertical = 8.dp),
                )
                Spacer(Modifier.height(14.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp, color = Accent)
                    Spacer(Modifier.width(10.dp))
                    Text("Жду подтверждения…", color = TextDim, style = MaterialTheme.typography.bodyMedium)
                }
                if (b.verificationUrl.isNotEmpty()) {
                    Spacer(Modifier.height(if (big) 28.dp else 20.dp))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        QrImage(
                            b.verificationUrl,
                            Modifier
                                .size(if (big) 150.dp else 112.dp)
                                .clip(RoundedCornerShape(12.dp))
                                .background(Color.White)
                                .padding(8.dp),
                        )
                        Spacer(Modifier.width(16.dp))
                        Text(
                            "Или наведите камеру\nтелефона — откроется\nподтверждение на сайте",
                            color = TextDim,
                            style = MaterialTheme.typography.bodyMedium,
                        )
                    }
                }
            }
        }
    }
}

/** Пошаговая подсказка: где подтвердить код. */
@Composable
fun DeviceCodeSteps(modifier: Modifier = Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Step(1, "Откройте kino на телефоне, где вы уже вошли")
        Step(2, "Профиль → «Подключить устройство»")
        Step(3, "Введите код с экрана — и всё")
    }
}

@Composable
private fun Step(n: Int, text: String) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Box(
            Modifier.size(28.dp).background(Surface2, RoundedCornerShape(50)),
            contentAlignment = Alignment.Center,
        ) { Text("$n", color = Accent, fontWeight = FontWeight.Bold) }
        Spacer(Modifier.width(12.dp))
        Text(text, color = TextMain, style = MaterialTheme.typography.bodyLarge)
    }
}

/** QR через zxing: ссылка на подтверждение устройства. */
@Composable
fun QrImage(text: String, modifier: Modifier = Modifier) {
    val bitmap = remember(text) { generateQr(text) }
    if (bitmap != null) {
        Image(bitmap.asImageBitmap(), "QR со ссылкой", modifier = modifier)
    }
}

private fun generateQr(text: String): Bitmap? = try {
    val size = 512
    val matrix = QRCodeWriter().encode(
        text, BarcodeFormat.QR_CODE, size, size,
        mapOf(com.google.zxing.EncodeHintType.MARGIN to 0),
    )
    val bmp = Bitmap.createBitmap(size, size, Bitmap.Config.RGB_565)
    val px = IntArray(size * size)
    for (y in 0 until size) for (x in 0 until size) {
        px[y * size + x] = if (matrix[x, y]) android.graphics.Color.BLACK else android.graphics.Color.WHITE
    }
    bmp.setPixels(px, 0, size, 0, 0, size, size)
    bmp
} catch (_: Exception) {
    null
}
