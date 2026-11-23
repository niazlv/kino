/** Иконки инлайном: ради десяти глифов тащить шрифт или пакет незачем. */
const p = (d: string) => (
  <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden><path d={d} /></svg>
)

export const IconPlay = () => p('M8 5v14l11-7z')
export const IconPause = () => p('M6 5h4v14H6zm8 0h4v14h-4z')
/** Перемотка на sec секунд: круговая стрелка и число внутри. Цифры —
 *  текстом, а не контуром: прежний контур рисовал от «10» одну единицу. */
export const IconSkip = ({ sec, fwd }: { sec: number; fwd?: boolean }) => (
  <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden>
    <path d={fwd ? 'M12 5V1l5 5-5 5V7a6 6 0 1 0 6 6h2a8 8 0 1 1-8-8z' : 'M12 5V1L7 6l5 5V7a6 6 0 1 1-6 6H4a8 8 0 1 0 8-8z'} />
    <text x="12" y="16.2" textAnchor="middle" fontSize="7.4" fontWeight="700"
          fontFamily="system-ui, sans-serif">{sec}</text>
  </svg>
)
export const IconPrev = () => p('M6 6h2v12H6zm3.5 6 8.5 6V6z')
export const IconArrowBack = () => p('M20 11H7.8l5.6-5.6L12 4l-8 8 8 8 1.4-1.4L7.8 13H20z')
/** Скорость: часы с «ветром» слева, как у кинопаба. */
export const IconSpeed = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden>
    <circle cx="14.5" cy="12" r="7" /><path d="M14.5 8.5V12l2.5 2" /><path d="M2 9h3.5M2 12h2.5M2 15h3.5" />
  </svg>
)
/** Громкость: одна волна — тише половины, две — громче. Одна волна
 *  на полной громкости читалась как «звук убавлен». */
export const IconVolumeLow = () => p('M3 9v6h4l5 5V4L7 9H3zm13.5 3a4.5 4.5 0 0 0-2.5-4v8a4.5 4.5 0 0 0 2.5-4z')
export const IconVolume = () =>
  p('M3 9v6h4l5 5V4L7 9H3zm13.5 3a4.5 4.5 0 0 0-2.5-4v8a4.5 4.5 0 0 0 2.5-4zM14 3.2v2.1a7 7 0 0 1 0 13.4v2.1a9 9 0 0 0 0-17.6z')
export const IconMute = () => p('M3 9v6h4l5 5V4L7 9H3zm18.3-1.3-1.4-1.4L17 9.2l-2.9-2.9-1.4 1.4L15.6 11l-2.9 2.9 1.4 1.4 2.9-2.9 2.9 2.9 1.4-1.4-2.9-2.9z')
export const IconSettings = () =>
  p('M19.4 13a7.8 7.8 0 0 0 0-2l2-1.6-2-3.4-2.4 1a7.6 7.6 0 0 0-1.7-1L15 3.4h-4l-.3 2.6c-.6.2-1.2.6-1.7 1l-2.4-1-2 3.4L4.6 11a7.8 7.8 0 0 0 0 2l-2 1.6 2 3.4 2.4-1c.5.4 1.1.8 1.7 1l.3 2.6h4l.3-2.6c.6-.2 1.2-.6 1.7-1l2.4 1 2-3.4-2-1.6zM13 15a3 3 0 1 1 0-6 3 3 0 0 1 0 6z')
export const IconPiP = () => p('M19 11h-8v6h8v-6zm4 8V5a2 2 0 0 0-2-2H3a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h18a2 2 0 0 0 2-2zm-2 0H3V5h18v14z')
export const IconFull = () => p('M7 14H5v5h5v-2H7v-3zm-2-4h2V7h3V5H5v5zm12 7h-3v2h5v-5h-2v3zM14 5v2h3v3h2V5h-5z')
export const IconExitFull = () => p('M5 16h3v3h2v-5H5v2zm3-8H5v2h5V5H8v3zm6 11h2v-3h3v-2h-5v5zm2-11V5h-2v5h5V8h-3z')
export const IconNext = () => p('M6 18l8.5-6L6 6v12zm10-12v12h2V6h-2z')
export const IconReplay = () =>
  p('M12 5V1L7 6l5 5V7a5 5 0 1 1-5 5H5a7 7 0 1 0 7-7z')
export const IconCheck = () => p('M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z')
