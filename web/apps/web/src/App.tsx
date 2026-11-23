import { useEffect, useRef, useState } from 'react'
import { api, ApiError, type Me, type Title } from './api'
import { useRoute, go, type Route } from './router'
import {
  NavContinue, NavDownloads, NavFresh, NavGenre, NavHistory, NavMore, NavMovie,
  NavNew, NavPlan, NavProfile, NavSearch, NavSeries, NavSettings, NavWatching,
} from './components/NavIcons'
import { Mark } from './components/Logo'
import { useSearchHistory, useTitleSuggest, SuggestRows } from './components/SearchSuggest'
import { Login } from './pages/Login'
import { Home } from './pages/Home'
import { TitlePage } from './pages/Title'
import { Search } from './pages/Search'
import { ListPage } from './pages/List'
import { NewEpisodes } from './pages/NewEpisodes'
import { History } from './pages/History'
import { Browse } from './pages/Browse'
import { LinkDevice } from './pages/LinkDevice'
import { Downloads } from './pages/Downloads'
import { Offline } from './pages/Offline'
import { Settings } from './pages/Settings'
import { Profile, initials } from './pages/Profile'
import { hasLibrary, resolveLocalTitles } from './native'
import { isTV } from './tv'

export function App() {
  const [me, setMe] = useState<Me | null>(null)
  const [loading, setLoading] = useState(true)
  const [offline, setOffline] = useState(false)
  const route = useRoute()
  // На телефоне меню — выдвижной ящик, а не постоянная полоса сверху: иначе
  // 35% экрана уходило на навигацию раньше, чем начиналось содержимое.
  const [navOpen, setNavOpen] = useState(false)

  useEffect(() => {
    api.me().then(m => {
      setMe(m)
      // Старым скачанным, найденным в папке, — номер тайтла (и постер), пока есть сеть.
      void resolveLocalTitles()
    }).catch(e => {
      // Вход — только тому, кого сервер не узнал. Если сервер не ответил
      // или лежит, вход по коду всё равно не сработает, а в приложении за
      // ним спряталось бы скачанное — ровно тогда, когда без него никак.
      if (e instanceof ApiError && (e.offline || e.status >= 500)) setOffline(true)
      else setMe(null)
    }).finally(() => setLoading(false))
  }, [])

  // Переход закрывает ящик: иначе после выбора раздела он остался бы поверх
  // того, ради чего его открывали.
  useEffect(() => { setNavOpen(false) }, [route])

  if (loading) return <div className="boot"><Mark /></div>
  if (offline) return <Offline />
  if (!me) {
    return (
      <Login
        initialInvite={route.name === 'invite' ? route.code : ''}
        onDone={() => {
          // Ссылка с инвайтом своё отработала: после входа — на главную.
          // Ссылка подтверждения устройства, напротив, остаётся: ради неё
          // и входили.
          if (route.name === 'invite') location.hash = '/'
          location.reload()
        }}
      />
    )
  }

  return (
    <div className="shell">
      <Sidebar route={route} open={navOpen} onClose={() => setNavOpen(false)} />
      <div className="main">
        <Topbar me={me} onMenu={() => setNavOpen(v => !v)} />
        <div className="content">
          <NetNote />
          {(route.name === 'home' || route.name === 'invite') && <Home />}
          {route.name === 'search' && <Search q={route.q} />}
          {route.name === 'list' && route.kind === 'following' && <NewEpisodes onlyNew={false} />}
          {route.name === 'list' && route.kind !== 'following' && <ListPage kind={route.kind} />}
          {route.name === 'new' && <NewEpisodes onlyNew />}
          {route.name === 'history' && <History page={route.page} />}
          {route.name === 'downloads' && <Downloads />}
          {route.name === 'settings' && <Settings />}
          {route.name === 'profile' && <Profile me={me} />}
          {route.name === 'browse' && <Browse kind={route.kind} genre={route.genre} />}
          {route.name === 'fresh' && <Browse kind="series" genre="" heading="Свежие серии" initialSort="updated" />}
          {route.name === 'link' && <LinkDevice code={route.code} />}
          {route.name === 'title' && (
            <TitlePage id={route.id} episodeId={route.episodeId}
                       season={route.season} episode={route.episode} />
          )}
        </div>
      </div>
    </div>
  )
}

function Sidebar({ route, open, onClose }:
  { route: Route; open: boolean; onClose: () => void }) {
  // На телефоне библиотека свёрнута по умолчанию, на широком экране открыта:
  // решение принимается один раз при монтировании, дальше им управляет человек.
  // Само сворачивание действует ТОЛЬКО в мобильной раскладке (см. .lib.closed
  // в стилях): иначе окно, открытое узким и растянутое потом, осталось бы
  // без библиотеки и без кнопки, которая могла бы её вернуть.
  const [libOpen, setLibOpen] = useState(
    () => typeof window === 'undefined' || !window.matchMedia('(max-width: 860px)').matches,
  )

  // На телевизоре меню открывается коротким: двадцать пунктов пультом
  // пролистываются вечно, а каждый день нужны пять. Остальное — за «Ещё»,
  // одним нажатием. В браузере всё как было: там прокрутка ничего не стоит.
  const [more, setMore] = useState(false)
  const full = !isTV() || more

  // Кнопка «Ещё» исчезает вместе с нажатием, и фокус падает на body:
  // раскрывшийся список должен принять его сам, иначе следующая стрелка
  // уводит человека в начало страницы.
  useEffect(() => {
    if (more) document.querySelector<HTMLElement>('.side [data-more-first]')?.focus()
  }, [more])

  const item = (to: string, label: string, on: boolean, icon: React.ReactNode, first = false) => (
    <a className={`nav${on ? ' on' : ''}`} href={`#${to}`} {...(first ? { 'data-more-first': '' } : {})}>
      <span className="ico">{icon}</span>
      {label}
    </a>
  )
  return (
    <>
      {/* Подложка только на телефоне: даёт закрыть ящик мимо ссылок. */}
      <div className={`navscrim${open ? ' on' : ''}`} onClick={onClose} />
      {/* Открытое положение дублируется инлайном: класс отвечает за плавность
          и за широкий экран, а состояние «открыто» задаётся напрямую, чтобы
          не зависеть от порядка правил. Меню, которое не открывается, на
          телефоне означает недоступную навигацию — цена ошибки высокая. */}
      <aside className={`side${open ? ' open' : ''}`}
             style={open ? { left: 0 } : undefined}>
      <div className="brand">
        {/* Знак и имя ведут на главную: так принято везде, и рука тянется
            сама. На телефоне заодно закрываем ящик: если человек уже на
            главной, маршрут не меняется и ящик сам не закроется. */}
        <a className="home" href="#/" onClick={onClose}><Mark /> kino</a>
        {/* Явное закрытие: подложка — не единственный выход, и на неё ещё
            надо догадаться нажать. */}
        <button className="navclose" onClick={onClose} aria-label="Закрыть меню">
          <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
            <path d="M6 6l12 12M18 6L6 18" stroke="currentColor"
                  strokeWidth="2" strokeLinecap="round" fill="none" />
          </svg>
        </button>
      </div>
      <h6>Моё</h6>
      {item('/', 'Продолжить смотреть', route.name === 'home', <NavContinue />)}
      {item('/new', 'Новые эпизоды', route.name === 'new', <NavNew />)}
      {item('/list/following', 'Я смотрю', route.name === 'list' && route.kind === 'following', <NavWatching />)}
      {full && item('/list/watchlist', 'Буду смотреть', route.name === 'list' && route.kind === 'watchlist', <NavPlan />, true)}
      {full && item('/history', 'История', route.name === 'history', <NavHistory />)}
      {/* Скачанное показывается только в приложении: в браузере смотреть
          нечего, а пустой раздел выглядел бы поломкой. */}
      {hasLibrary() && item('/downloads', 'Скачанное', route.name === 'downloads', <NavDownloads />)}
      {/* Короткое меню телевизора кончается здесь: поиск — вход в каталог,
          он нужен чаще всего, а всё прочее прячется за одну строку. */}
      {!full && (
        <>
          {item('/search', 'Поиск по каталогу', route.name === 'search', <NavSearch />)}
          <button className="nav" onClick={() => setMore(true)}>
            <span className="ico"><NavMore /></span>
            Ещё
          </button>
        </>
      )}
      {full && (
        <>
      <h6 className="grouphead">
        Библиотека
        {/* Кнопка видна только на узком экране: там развёрнутая библиотека
            занимала 327px — сорок процентов экрана телефона до начала
            содержимого. На широком сворачивать нечего, места хватает. */}
        <button className="libtoggle" aria-expanded={libOpen}
                onClick={() => setLibOpen(v => !v)}>
          {libOpen ? 'свернуть' : 'показать'}
        </button>
      </h6>
      <div className={`lib${libOpen ? '' : ' closed'}`}>
        {item('/browse//', 'Новинки', route.name === 'browse' && !route.kind && !route.genre, <NavFresh />)}
        {item('/fresh', 'Свежие серии', route.name === 'fresh', <NavNew />)}
        {item('/browse/movie/', 'Фильмы', route.name === 'browse' && route.kind === 'movie' && !route.genre, <NavMovie />)}
        {item('/browse/series/', 'Сериалы', route.name === 'browse' && route.kind === 'series' && !route.genre, <NavSeries />)}
        <Genres route={route} />
      </div>
      {/* Поиск НЕ прячется: это вход в каталог, и он нужен чаще всего. */}
      {item('/search', 'Поиск по каталогу', route.name === 'search', <NavSearch />)}
      {item('/settings', 'Настройки', route.name === 'settings', <NavSettings />)}
      {item('/profile', 'Профиль', route.name === 'profile', <NavProfile />)}
        </>
      )}
      </aside>
    </>
  )
}

/**
 * Жанры подтягиваются из каталога, а не задаются списком в коде: он растёт
 * вместе с обходом, и вшитый перечень разошёлся бы с содержимым.
 */
function Genres({ route }: { route: Route }) {
  const [genres, setGenres] = useState<string[]>([])
  useEffect(() => { api.genres().then(r => setGenres(r.items ?? [])).catch(() => {}) }, [])
  return (
    <>
      {genres.slice(0, 8).map(g => (
        <a key={g}
           className={`nav${route.name === 'browse' && route.genre === g ? ' on' : ''}`}
           href={`#/browse//${encodeURIComponent(g)}`}>
          <span className="ico"><NavGenre /></span>
          {g}
        </a>
      ))}
    </>
  )
}

function Topbar({ me, onMenu }: { me: Me; onMenu: () => void }) {
  const [q, setQ] = useState('')
  // Подсказки поиска: недавние запросы под строкой, как в YouTube, а пока
  // набирают — и подходящие тайтлы каталога, популярные первыми.
  // Открываются по фокусу, гаснут по Escape и щелчку мимо списка.
  const hist = useSearchHistory()
  const [open, setOpen] = useState(false)
  const [hi, setHi] = useState(-1)
  const input = useRef<HTMLInputElement>(null)
  const v = q.trim().toLowerCase()
  // Пока набирают, из истории — лишь пара совпавших запросов: главное
  // тогда — тайтлы под ними.
  const sug = v ? hist.items.filter(h => h.toLowerCase().includes(v)).slice(0, 3) : hist.items.slice(0, 8)
  const titles = useTitleSuggest(open ? q : '')
  const rows = sug.length + titles.length
  // Тайтлы приезжают позже буквы: подсветка не должна остаться за концом.
  useEffect(() => { if (hi >= rows) setHi(-1) }, [hi, rows])
  const close = () => {
    setOpen(false); setHi(-1)
    // Фокус снимаем: на телефоне иначе клавиатура оставалась поверх
    // результатов, а шапка — свёрнутой при живом фокусе в поле.
    input.current?.blur()
  }
  const pick = (h: string) => {
    setQ(h); close()
    void hist.remember(h)
    go(`/search/${encodeURIComponent(h)}`)
  }
  // Тайтл из подсказки открывается сразу: это не поиск, и недописанное
  // «дю» в историю запросов не попадает.
  const openTitle = (t: Title) => {
    setQ(''); close()
    go(`/title/${t.id}`)
  }
  return (
    // «searching» на телефоне отдаёт поиску всю шапку: рядом с меню,
    // админкой, аватаром и «Выйти» полю доставалось ~110px, а подсказки той
    // же ширины резали запросы до одной буквы.
    <header className={`topbar${open ? ' searching' : ''}`}>
      {/* Видна только на телефоне: на широком экране меню и так на месте. */}
      <button className="menubtn" onClick={onMenu} aria-label="Меню">
        <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
          <path d="M4 7h16M4 12h16M4 17h16" stroke="currentColor"
                strokeWidth="2" strokeLinecap="round" fill="none" />
        </svg>
      </button>
      {/* Выход из раскрытого поиска — только на телефоне: кнопки шапки на это
          время спрятаны, и тапать «куда-нибудь мимо» неочевидно. Фокус
          уходит из поля уже на нажатии, onClick — на случай, когда кнопка
          фокуса не берёт. */}
      <button type="button" className="sback" aria-label="Закрыть поиск"
              onClick={() => { setOpen(false); input.current?.blur() }}>
        <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
          <path d="M19 12H5M11 6l-6 6 6 6" stroke="currentColor"
                strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
        </svg>
      </button>
      <form
        className="search"
        onSubmit={e => { e.preventDefault(); setOpen(false); if (q.trim().length >= 2) pick(q.trim()) }}
      >
        <span className="sico" aria-hidden>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <path d="M11 4.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13zM16 16l4 4" />
          </svg>
        </span>
        {/* Пока список открыт, стрелки вверх-вниз — его: общий движок
            стрелок иначе уводил фокус из поля на кнопку подсказки, и
            набранное больше не правилось. */}
        <input ref={input} placeholder="Название или имя…" value={q}
               data-arrows={open && rows ? 'y' : undefined}
               onChange={e => { setQ(e.target.value); setOpen(true); setHi(-1) }}
               onFocus={() => { setOpen(true); hist.refresh() }}
               onClick={() => setOpen(true)}
               onBlur={e => {
                 // Щелчок по подсказке фокус не уводит (mousedown отменён);
                 // гасим список только при уходе фокуса совсем.
                 if (!(e.relatedTarget instanceof Node) || !e.currentTarget.form?.contains(e.relatedTarget)) setOpen(false)
               }}
               onKeyDown={e => {
                 if (e.key === 'ArrowDown' && rows) {
                   e.preventDefault(); setOpen(true); setHi(h => Math.min(h + 1, rows - 1))
                 } else if (e.key === 'ArrowUp') {
                   e.preventDefault(); setHi(h => Math.max(h - 1, -1))
                 } else if (e.key === 'Enter' && hi >= 0 && hi < rows) {
                   e.preventDefault()
                   const s = sug[hi]; const t = titles[hi - sug.length]
                   if (s) pick(s); else if (t) openTitle(t)
                 } else if (e.key === 'Escape') setOpen(false)
               }}
               aria-label="Поиск по каталогу" />
        {open && (
          <SuggestRows items={sug} titles={titles} hi={hi} onHover={setHi} onPick={pick}
                       onTitle={openTitle} onForget={h => void hist.forget(h)}
                       onClear={!v && hist.items.length ? () => void hist.clear() : undefined} />
        )}
      </form>
      <span className="spacer" />
      {/* Админка живёт тем же доменом под /admin; ссылка — только тем,
          кому она откроется. Отдельный вход там всё равно спросит TOTP. */}
      {me.role === 'admin' && <a className="btn ghost sm" href="/admin/">Админка</a>}
      {/* Имя ведёт в профиль: там подключение устройств по коду, пароль
          и выход. «Выйти» в шапке больше не нужен — он там же, а шапке
          телефона каждая кнопка стоит места под поиск. */}
      <a className="userchip" href="#/profile" title="Профиль">
        <span className="avatar">{initials(me)}</span>
        <span className="uname">{me.displayName || me.login}</span>
      </a>
    </header>
  )
}

/**
 * Связь пропала, пока витрина открыта: страницы перестают грузиться, и
 * без подсказки это выглядит поломкой. В приложении скачанное при этом
 * смотрится как обычно — туда и ведём.
 */
function NetNote() {
  const [online, setOnline] = useState(() => navigator.onLine)
  useEffect(() => {
    const on = () => setOnline(navigator.onLine)
    addEventListener('online', on)
    addEventListener('offline', on)
    return () => { removeEventListener('online', on); removeEventListener('offline', on) }
  }, [])
  if (online) return null
  return (
    <div className="netnote">
      Нет интернета.{' '}
      {hasLibrary()
        ? <>Скачанное смотрится и без него — <a href="#/downloads">Скачанное</a></>
        : 'Страницы откроются, когда связь вернётся.'}
    </div>
  )
}

/** Истёкшая сессия — это не ошибка, а приглашение войти. */
export function describeError(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.unauthorized) return 'сессия истекла — войдите заново'
    if (e.offline) return 'нет связи с сервером — проверьте интернет'
    if (e.status === 404) return 'не найдено'
    if (e.status === 502) return 'площадка недоступна, попробуйте позже'
    // 503 приходит, когда площадка ограничила темп запросов. Это проходит
    // само за минуту, поэтому зовём повторить, а не сообщаем о поломке.
    if (e.status === 503) return 'площадка просит притормозить — попробуйте ещё раз через минуту'
    if (e.status === 504) return 'площадки не ответили — попробуйте ещё раз'
    return e.message || 'ошибка'
  }
  return e instanceof Error ? e.message : 'ошибка'
}
