# Прогоны витрины

Два стенда. Первый — браузер под управлением тестов: быстрый, гоняется на
каждую правку. Второй — настоящее приложение на телевизоре: медленный, но
только он видит то, чего в браузере не бывает.

Почему второй нужен: у WebView телевизора есть СВОЯ навигация стрелками.
Всё, что витрина не перехватила, она разбирает по-своему — уводит фокус
наискосок, из меню в сетку фильмов. В браузере на компьютере стрелки просто
прокручивают страницу, и эта беда там не воспроизводится никак.

Лежит вне рабочего пространства pnpm (в `pnpm-workspace.yaml` только
`packages/*` и `apps/*`) и вне образа витрины (`web.Dockerfile` копирует
только их) — в сборку и в бой Playwright не попадает.

## Браузер

Нужен поднятый стенд и пользователь `tvtest` (пароль в `helpers.js`).
Завести один раз:

```bash
docker compose exec -T postgres psql -U kino -d kino -c \
  "INSERT INTO invites (code, created_by, expires_at) VALUES ('TVFOCUS1', (SELECT MIN(id) FROM users), now() + interval '1 day') ON CONFLICT DO NOTHING;"
curl -s -X POST http://localhost:5173/api/v1/auth/register -H 'content-type: application/json' \
  -d '{"invite":"TVFOCUS1","login":"tvtest","password":"tvtest-parol-1","displayName":"ТВ-проверка"}'
```

```bash
cd web/e2e && npm install && npx playwright install chromium
npm test
```

Окно — 1920×1080 при плотности 1: на такой комбинации витрина включает
масштаб раскладки, как в браузере телевизора.

- `tv.spec.js` — меню, фокус, подсветка, поиск, настройки
- `player.spec.js` — пульт в плеере с настоящим воспроизведением, полный
  экран через смену серии без жеста и при выборе серии из списка
- `sources.spec.js` — площадки, дополняющие друг друга: склеенный тайтл
  играет kinopub, переключение на AnimeGO.me с того же места, меню озвучек;
  тайтл только с AnimeGO.fr. Номера тайтлов — стенда (ADR-0008)
- `perf.spec.js` — во что обходится одно нажатие
- `old-engine.spec.js` — витрина на движке, который не знает `color-mix`,
  `aspect-ratio` или `:focus-visible`: стили на лету лишаются таких
  объявлений и правил, ровно как их выбросил бы телевизор постарше

## Настоящий телевизор

Нажатия идут пультом через `adb`, состояние читается из WebView приложения
по отладочному протоколу.

```bash
# эмулятор Android TV (или включённая отладка по USB на телевизоре)
~/Library/Android/sdk/emulator/emulator -avd kinotv -no-audio -no-boot-anim &
adb -s emulator-5556 shell am start -n ru.sorewa.kino/.MainActivity

# отладочный сокет WebView -> локальный порт
adb -s emulator-5556 shell "cat /proc/net/unix | grep -o 'webview_devtools_remote.*'"
adb -s emulator-5556 forward tcp:9333 localabstract:webview_devtools_remote_<pid>

npm run device
```

Устройство и порт переопределяются переменными `KINO_DEV` и `KINO_CDP`.
Отладочная сборка оболочки должна смотреть на стенд:
`KINO_URL=http://10.0.2.2:5173 pnpm apk:debug`.

Витрина в приложении кешируется — после пересборки стенда страницу нужно
перезагрузить, иначе проверяется старая сборка:

```bash
node -e "import('./tvbox.mjs').then(async m => { const a = await m.open(); await a.\$(\`(location.href='http://10.0.2.2:5173/?n='+Date.now(),1)\`); a.close() })"
```
