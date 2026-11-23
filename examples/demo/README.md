# examples/demo — пример площадки

Площадка «Открытые фильмы»: четыре короткометражки Blender Foundation под
лицензией CC BY. Каталог лежит в коде, ключей и VPN не нужно. Это
**пример протокола площадки** (ADR-0001, ADR-0017, ADR-0018). В сборке без
приватных площадок он же каталог по умолчанию: включается, когда
`PROVIDERS` не задан.

## Как написать свою площадку на Go

1. Пакет рядом, например `examples/mysite` (в полном дереве — `providers/mysite`).
   Провайдер реализует `appprov.Provider` из `internal/app/provider`:
   `Describe`, `Search`, `Title`, `Episodes`, `ChangedSince`, `Resolve`. По
   желанию — `Lookup`, `Voices`, `Segments`, `Thumbnails`, но каждую
   возможность, заявленную в `Describe`, надо реализовать.
2. `spec.go` с `source.Register(source.Spec{…})`: id, имя, пометка для
   экрана, вид каталога (`general`/`anime`), настройки (`Settings`, читаются
   через `env.Setting`) и `New`. HTTP — только клиентом из `source.Env.HTTP`:
   так запросы идут маршрутом площадки, с её подписью и счётчиком.
3. Подключить пакет одной строкой, как `cmd/*/demo.go`.
4. Проверить исполняемым контрактом: тест как в `demo_test.go`
   (`providertest.Run`) или `go run ./cmd/sourcekit check <id> -q "запрос" -fetch`.

Что площадке можно знать о ядре — только протокол: `internal/domain/*`,
`internal/app/provider`, `internal/app/matching`,
`internal/adapter/providers/{source,sitekit,providertest}`, `pkg/*`. Это
проверяет `make boundaries`.

Площадку можно написать и одним файлом на JavaScript, без Go и пересборки
(`docs/script-sources.md`).

## Фильмы, лицензии и откуда потоки

Все четыре фильма — проекты Blender Foundation (Blender Institute, Амстердам),
лицензия [CC BY](https://creativecommons.org/licenses/by/3.0/): их можно
показывать и распространять с указанием авторства.

| Фильм | Авторы | Лицензия фильма и постера | Поток (тестовая копия) |
|---|---|---|---|
| Elephants Dream (2006) | Bassam Kurdali, Andy Goralczyk и Blender Foundation | CC BY 2.5; постер — CC BY 4.0 | THEOplayer |
| Big Buck Bunny (2008) | Blender Foundation, peach.blender.org | CC BY 3.0 | Mux |
| Sintel (2010) | Blender Foundation, durian.blender.org | CC BY 3.0 | THEOplayer |
| Tears of Steel (2012) | Blender Foundation, Project Mango | CC BY 3.0 | Unified Streaming |

Постеры — Wikimedia Commons (`Big_buck_bunny_poster_big.jpg`,
`ElephantsDreamPoster.jpg`, `Sintel_poster.jpg`, `Tos-poster.png`), уменьшенные
копии самого Commons. Потоки — публичные тестовые копии, которые держат у
себя разработчики видеоплееров. Пропадёт поток — фильм не откроется, а
площадка останется рабочим примером. Свой адрес — правкой `catalog` в
`demo.go`.
