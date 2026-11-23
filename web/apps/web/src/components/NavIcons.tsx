/**
 * Иконки навигации — инлайновым SVG.
 *
 * Без шрифта и без пакета: глифов полтора десятка, а зависимость ради них
 * тянет мегабайт и ещё один источник отказа. Все на одной сетке 24×24
 * с обводкой, чтобы вес штриха читался одинаково.
 */
const stroke = (d: string) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7"
       strokeLinecap="round" strokeLinejoin="round" aria-hidden>
    {d.split('|').map((p, i) => <path key={i} d={p} />)}
  </svg>
)

export const NavContinue = () => stroke('M5 4.5v15l13-7.5z')
export const NavNew = () => stroke('M12 3a6 6 0 0 0-6 6v4l-2 3h16l-2-3V9a6 6 0 0 0-6-6z|M9.5 19a2.5 2.5 0 0 0 5 0')
export const NavWatching = () => stroke('M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z|M12 9.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5z')
export const NavPlan = () => stroke('M6 3.5h12v17l-6-4-6 4z')
export const NavHistory = () => stroke('M3.5 12a8.5 8.5 0 1 0 2.6-6.1|M3.5 4.5V10h5.5|M12 7.5V12l3 2')
export const NavFresh = () => stroke('M12 2.5l2.2 4.6 5 .7-3.6 3.6.9 5-4.5-2.4-4.5 2.4.9-5L4.8 7.8l5-.7z')
export const NavMovie = () => stroke('M3 6.5h18v12H3z|M7 6.5l-2 12|M12 6.5l-1 12|M17 6.5l1 12')
export const NavSeries = () => stroke('M3 5.5h18v11H3z|M8 20.5h8')
export const NavGenre = () => stroke('M4 7h16|M4 12h16|M4 17h10')
export const NavSearch = () => stroke('M11 4.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13z|M16 16l4 4')
export const NavDownloads = () => stroke('M12 3.5v10|M8 10.5l4 4 4-4|M4.5 17.5v3h15v-3')
/** «Ещё»: галка вниз — раскрывает второй уровень меню. */
export const NavMore = () => stroke('M5 9l7 7 7-7')
export const NavSettings = () => stroke('M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z|M4 12h2.2|M17.8 12H20|M12 4v2.2|M12 17.8V20|M6.7 6.7l1.6 1.6|M15.7 15.7l1.6 1.6|M17.3 6.7l-1.6 1.6|M8.3 15.7l-1.6 1.6')
export const NavProfile = () => stroke('M12 4a4 4 0 1 0 0 8 4 4 0 0 0 0-8z|M4.5 20.5a7.5 7.5 0 0 1 15 0')

/** Устройства в «Где вы вошли»: телевизор, телефон, компьютер. */
export const DevTv = () => stroke('M3 5.5h18v11H3z|M8 20.5h8')
export const DevPhone = () => stroke('M8 2.5h8a1.5 1.5 0 0 1 1.5 1.5v16a1.5 1.5 0 0 1-1.5 1.5H8A1.5 1.5 0 0 1 6.5 20V4A1.5 1.5 0 0 1 8 2.5z|M11 18.5h2')
export const DevComputer = () => stroke('M3.5 4.5h17v11h-17z|M9 20h6|M12 15.5V20')
