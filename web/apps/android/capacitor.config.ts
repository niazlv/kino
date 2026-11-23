import type { CapacitorConfig } from '@capacitor/cli'

/**
 * Android-оболочка: системный WebView, который открывает витрину с вашего
 * сервера.
 *
 * Своего интерфейса здесь нет намеренно. Витрина одна и обновляется обычным
 * деплоем; APK пересобирается только при правке самой оболочки. Иначе
 * получились бы две подплатформы, которые расходятся при каждом изменении.
 *
 * Адрес сервера — только при сборке (KINO_URL), в исходниках его нет:
 * publish-apk.sh --shell передаёт https://$DOMAIN из deploy/prod/local.env.
 * Для стенда: KINO_URL=http://10.0.2.2:5173 (адрес хоста из эмулятора),
 * тогда же включается cleartext — иначе WebView не откроет http.
 */
const url = process.env.KINO_URL ?? ''
if (!url) {
  throw new Error('KINO_URL не задан: адрес сервера, который откроет оболочка (KINO_URL=https://… pnpm exec cap sync android)')
}

const config: CapacitorConfig = {
  appId: 'ru.sorewa.kino',
  appName: 'kino',
  // Локально лежит только страница «нет связи»; витрина живёт на сервере.
  webDir: 'www',
  server: {
    url,
    cleartext: url.startsWith('http://'),
    errorPath: 'error.html',
  },
  android: {
    backgroundColor: '#0e1116',
    allowMixedContent: false,
  },
  plugins: {
    // Светлые значки на тёмных панелях. Отступы под панели задаёт сама
    // оболочка (MainActivity): витрина объявляет viewport-fit=cover, и в
    // режиме css Capacitor отдал бы ей вставки, которых она не использует.
    SystemBars: { style: 'DARK', insetsHandling: 'disable' },
  },
}

export default config
