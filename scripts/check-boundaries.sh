#!/usr/bin/env bash
# Архитектурные инварианты, которые дороже всего восстанавливать задним числом.
# depguard в .golangci.yml ловит их на уровне пакетов; здесь — на уровне
# транзитивных зависимостей собранных бинарников и на уровне web.
set -uo pipefail
cd "$(dirname "$0")/.."
MOD=github.com/niazleushkin/kino
fail=0
ok()   { printf '  \033[32mok\033[0m   %s\n' "$1"; }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=1; }

echo "== ADR-0002: media-gateway ничего не знает о провайдерах =="
# Транзитивно: даже если провайдерский пакет придёт через третьи руки.
PROV="$MOD/(providers|internal/adapter/providers|internal/gen/kino/provider)(/|$)"
if go list -deps ./cmd/gateway 2>/dev/null | grep -qE "$PROV"; then
  bad "cmd/gateway транзитивно тянет провайдерский код"
  go list -deps ./cmd/gateway | grep -E "$PROV" | sed 's/^/       /'
else
  ok "cmd/gateway чист от провайдеров -> добавление платформы не требует его передеплоя"
fi

echo "== ADR-0018: ядро не знает площадок =="
# Площадки (providers/) уезжают в приватный репозиторий. Ядро — всё, кроме
# них, — встречается с ними только в cmd/*/providers.go (тег !noproviders).
# Без этих файлов не должно остаться ни одного пути к providers/, и в тестах тоже.
CORE="./cmd/... ./internal/... ./pkg/..."
leak=$(go list -deps -test -tags noproviders $CORE 2>/dev/null | grep -E "^$MOD/providers(/|$)" | sort -u)
if [ -n "$leak" ]; then
  bad "ядро тянет код площадок мимо cmd/*/providers.go"
  echo "$leak" | sed 's/^/       /'
else
  ok "ядро не импортирует providers/ -> каталог отделяется как есть"
fi
if out=$(go build -tags noproviders ./cmd/... 2>&1); then
  ok "бинарники собираются без площадок (-tags noproviders)"
else
  bad "без площадок бинарники не собираются"
  echo "$out" | sed 's/^/       /'
fi

echo "== ADR-0018: площадки (и пример examples/) видят только протокол =="
# Что площадке можно знать о ядре — это и есть протокол плагинов для Go:
# доменные типы, порты app/provider, разбор названий, реестр source, общий
# HTTP и мост плееров sitekit, контракт-тесты providertest. Сборке
# providers/all ещё можно подключать хост скриптов (script) и, в тесте,
# смотреть реестр сборки.
CONTRACT="^$MOD/(internal/domain/(kernel|provider|catalog|playback)|internal/app/(provider|matching)|internal/adapter/providers/(source|sitekit|providertest)|pkg/.+)$"
viol=$(go list -f '{{.ImportPath}}{{range .Imports}} {{.}}{{end}}{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}' ./providers/... ./examples/... 2>/dev/null |
  while read -r pkg deps; do
    for d in $deps; do
      # Свои пакеты и чужие модули — не про ядро. (Скобка перед образцом —
      # иначе bash 3.2 с macOS путает её с концом $(...).)
      case "$d" in ("$MOD"/providers|"$MOD"/providers/*|"$MOD"/examples/*) continue ;; ("$MOD"/*) ;; (*) continue ;; esac
      if [ "$pkg" = "$MOD/providers/all" ]; then
        case "$d" in ("$MOD"/internal/adapter/providers/script|"$MOD"/internal/bootstrap/providers) continue ;; esac
      fi
      echo "$d" | grep -qE "$CONTRACT" || echo "$pkg -> $d"
    done
  done | sort -u)
if [ -n "$viol" ]; then
  bad "площадки лезут в ядро мимо протокола"
  echo "$viol" | sed "s#$MOD/##g; s/^/       /"
else
  ok "площадки импортируют из ядра только протокол (domain, app/provider, source, sitekit, pkg/…)"
fi

echo "== Слой domain не тянет инфраструктуру =="
if go list -deps ./internal/domain/... 2>/dev/null | grep -qE '^(github.com/jackc|github.com/redis|google.golang.org/grpc)'; then
  bad "internal/domain тянет драйверы"
else
  ok "internal/domain изолирован"
fi

echo "== pkg/ не знает про internal/ =="
if go list -deps ./pkg/... 2>/dev/null | grep -q "$MOD/internal"; then
  bad "pkg/ зависит от internal/ — библиотека не отделяется"
else
  ok "pkg/ отделяем как есть"
fi

echo "== Плеер отделяем: @kino/player не зависит от приложения =="
PJ=web/packages/player/package.json
if [ -f "$PJ" ]; then
  if node -e '
    const p=require("./'"$PJ"'");
    const d={...p.dependencies,...p.peerDependencies};
    const bad=Object.keys(d||{}).filter(k=>k.startsWith("@kino/"));
    if(bad.length){console.error(bad.join(", "));process.exit(1)}
  ' 2>/dev/null; then
    ok "@kino/player не зависит ни на один внутренний пакет -> публикуется в npm как есть"
  else
    bad "@kino/player зависит от внутренних пакетов — отделяемость сломана"
  fi
  if grep -rqE "from ['\"](@kino/api-client|@/)" web/packages/player/src 2>/dev/null; then
    bad "в исходниках плеера есть импорты приложения"
  else
    ok "в исходниках плеера нет импортов приложения"
  fi
fi

exit $fail
