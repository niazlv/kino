# Площадки-скрипты

Площадка — один файл `.js` (ADR-0017). Положили его в каталог площадок-скриптов
(`SOURCES_DIR` у сервера; вшитые в сборку — `providers/sources/`) — движок на
устройстве и сервер подхватывают его сами, без строчки кода на Go. На
телефоне тот же файл ставится в «Локальный движок» → «Площадки».

```bash
go run ./cmd/sourcekit new mysite              # заготовка: свои потоки
go run ./cmd/sourcekit new mysite -players     # заготовка: чужие плееры
go run ./cmd/sourcekit check mysite.js -q "название" -fetch
go run ./cmd/sourcekit list                    # все площадки: Go и скрипты
go run ./cmd/sourcekit import ~/Downloads/mysite.js -q "название"   # принять присланное
```

`check` гоняет тот же набор проверок, что принимает площадку у нас и на
телефоне (`internal/adapter/providers/providertest`). Пока он не пишет
«ИТОГ: годится» — площадку не принимаем. Ключи площадки и маршрут наружу —
из окружения: `FILMIX_TOKEN=… EGRESS_ROUTES=default=socks5://127.0.0.1:1080 sourcekit check …`.

Пример целиком — [animevost.js](animevost.js): открытый API, свои MP4.

## Описание

```js
source({
  id: "mysite",            // имя в PROVIDERS и в привязках: выбирается один раз
  name: "MySite",          // как назвать человеку
  version: "1.0.0",        // растёт с каждой правкой — так видно, что новее
  kind: "anime",           // anime — весь каталог аниме; general — фильмы и сериалы
  note: "Аниме: Kodik и CVH. Kodik — только с российского адреса.",
  hosts: ["mysite.org"],   // куда скрипту можно ходить (поддомены тоже)
  home: "https://mysite.org/",   // проверка живости; или метод health()
  settings: [{ env: "MYSITE_TOKEN", label: "Токен", secret: true }],
  thumbs: true,            // путь «чужие плееры»: у CVH есть кадры серий
  subtitles: true,         // внешние субтитры (Alloha)
  gap: 300,                // пауза между запросами к одному хосту, мс

  search(query, limit) { … },   // обязателен
  title(id) { … },              // обязателен
  // дальше — один из двух путей
});
```

`id` — латиница в нижнем регистре, цифры и `_`. Совпал с площадкой на Go —
скрипт её заменяет (так чинят встроенную площадку без новой сборки; история
просмотра сохраняется, привязки те же).

## Тайтл

`search` возвращает массив тайтлов (лучшие первыми, не больше `limit`),
`title(id)` — один.

| поле | что |
|---|---|
| `id` | номер у площадки (строка или число) — обязателен |
| `title`, `original`, `alt` | название, оригинальное, прочие (массив) — нужны склейке |
| `kind` или `movie: true` | `movie` / `series` |
| `season` | какой сезон описывает страница (у аниме-площадок страница = сезон) |
| `year`, `plot`, `poster`, `genres`, `countries` | как есть, без HTML (`html.text`) |
| `kp`, `imdb`, `mal` | номера Кинопоиска, IMDb, MyAnimeList — лучшие ключи склейки |
| `finished`, `aired`, `popularity` | закончен; сколько серий вышло; сколько людей знает (голоса) |
| `episodes`, `durations`, `minutes` | путь «чужие плееры»: номера серий, длина `{"1": 1420}`, оценка длины |

Нет такого — `throw NotFound("…")`; площадка не отвечает —
`throw Unavailable("…")`; просит подождать — `throw Busy()`. Прочие ошибки
считаются поломкой скрипта (для плеера — «площадка недоступна, играем с
другой»).

## Путь 1: свои потоки

```js
episodes(id) { return [{ season: 1, episode: 1, title: "…", duration: 1420, thumb: "…" }]; },
resolve(req) {   // req: {id, season, episode, variant, variantLabel, maxHeight}
  return {
    url: "https://cdn…/master.m3u8",   // или .mp4
    protocol: "HLS",                    // или "PROGRESSIVE"; пусто — по адресу
    headers: { Referer: "https://mysite.org/" },   // нужны CDN — шлюз их подставит
    origins: ["cdn.example"],           // откуда можно тянуть сегменты; пусто — домен url
    ttl: 6 * 3600,                       // или expires: "2026-…Z"
    variants: [{ id: "studio1", title: "AniLibria" }], variant: "studio1",
    audios: [{ id: "0", language: "rus", title: "AniLibria" }],
    subtitles: [{ url: "https://…/ru.vtt", language: "rus", title: "Русские" }],
    heights: [1080, 720],
    segments: [{ kind: "intro", start: 90, end: 180 }],   // секунды
  };
},
voices(req) { return [{ id: "studio1", title: "AniLibria" }]; },   // по желанию
```

Серии у фильма — одна: `{season: 0, episode: 1}`. У сериала, где страница =
сезон, серии идут в сезоне 1, а какой это сезон произведения — говорит
`title.season`.

## Путь 2: чужие плееры

```js
players(id, episode) {
  return {
    kodik: "https://kodik.info/serial/…",     // iframe Kodik
    cvh: { pub: "…", mal: "52991" },          // CDNVideoHub по номеру MAL
    alloha: "https://…",                      // iframe балансера Alloha
    referer: "https://mysite.org/anime/" + id,
  };
},
```

Хотя бы один плеер. Серии, озвучки, поток, разметку пропуска и кадры
добудет платформа — тем же кодом, что у площадок «Джутсу» и YummyAnime.

## Необязательное

- `lookup({imdb, kp, title, original, year, kind})` — найти произведение по
  номерам одним запросом (досбор к любому открытому тайтлу).
- `changed(since, emit)` — обход для сервера: `emit(тайтл)` на каждый;
  `since` — ISO-время или пусто (полный обход). Без него площадка живёт
  только поиском — как на телефоне всегда.
- `health()` — своя проверка живости вместо GET `home`.

## Что есть у скрипта

| | |
|---|---|
| `http.get(url, opts)` / `http.post(url, opts)` | ответ `{status, ok, url, text, headers, json()}` |
| `http.json(url, opts)` / `http.text(url, opts)` | сразу тело; 404 → NotFound, 429 → Busy, прочее не 2xx → Unavailable |
| `opts` | `{headers, query: {…}, form: {…}, json: {…}, body, referer, method}`; `form`/`json` — это POST |
| `html.parse(text, base)` | документ: `find(css)` → массив, `first(css)` → узел или null, `text()`, `attr(a)`, `abs(a)` (абсолютный адрес), `html()` |
| `html.text(fragment)` | HTML как текст: без тегов, с раскрытыми `&amp;` |
| `url.abs(base, ref)`, `url.host(u)`, `url.param(u, name)`, `url.query(obj)` | адреса |
| `text.int(s)`, `text.year(s)`, `text.between(s, a, b)` | разбор |
| `base64.encode/decode`, `JSON`, `RegExp` | как в JS |
| `cache.get(key)`, `cache.put(key, value, ttlSec)` | память площадки, общая на все вызовы |
| `settings.get("MYSITE_TOKEN")` | только объявленные в `settings` |
| `players.sibnet(url, referer)`, `players.aniboom(url, referer)` | готовый ответ `resolve` для этих плееров |
| `log.info/warn/debug(…)` | в журнал движка |

Ходить можно только на `hosts` (и их поддомены, в том числе после
редиректа). Каждый вызов ограничен по времени. User-Agent — браузерный,
выставляет платформа.

## Как правка попадает на сервер

1. Человек ставит файл в приложении, жмёт «Проверить» и «Отправить нам» —
   файл и отчёт уходят в мессенджер.
2. `go run ./cmd/sourcekit import файл.js -q "запрос"` — разбор, версия
   новее нашей, проверки, копия сюда.
3. `git diff`, коммит, выкатка core и crawler; включение — `PROVIDERS`.
   Следующая сборка приложения несёт площадку всем.
