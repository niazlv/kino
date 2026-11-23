# Иконка

Знак — билет с кнопкой «играть» на зелёной плашке. Выбран 2026-09-05 из
четырёх кандидатов (`preview.html`, сравнение финалистов — `compare.html`).

- `gen.py` — единственный источник геометрии. Пересобирает `candidates/`
  и превью; с флагом `--export` пишет боевые файлы в `web/apps/web/public/`
  (SVG-фавикон, apple-touch-icon 180, PNG 192/512, maskable 512) и копию
  SVG в `internal/transport/httpadmin/static/` — у админки своя статика.
  Для PNG нужен `rsvg-convert` (`brew install librsvg`).
- Тот же контур вшит в `web/apps/web/src/components/Logo.tsx` и в знак
  шапки шаблонов админки (`layout`, `login`, `factor`, `enroll`): при правке
  геометрии обновить и их (путь берётся из `candidates/ticket-mono.svg`).
- Для Android TV понадобится ещё баннер 320×180 — рисовать из того же `gen.py`.
