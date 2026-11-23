#!/usr/bin/env bash
#
# Публичный снимок собирается сам по себе (ADR-0018).
#
#   scripts/check-public-snapshot.sh <каталог снимка>
#
# Снимок — дерево без площадок (providers/, cmd/*/providers.go), как его
# кладёт scripts/publish-public.sh. Скрипт:
#   1. делает снимок самостоятельным: убирает из go.mod require и replace
#      kinopub-downloader (его требуют только площадки; replace смотрит на
#      соседний каталог, которого у чужого человека нет) и запускает go mod tidy;
#   2. проверяет, что в снимке не осталось ни площадок, ни ссылок на них;
#   3. собирает и тестирует: go build, go vet, go test, make boundaries;
#   4. предупреждает о том, что не ломает сборку Go, но мешает человеку
#      со снимком (Docker-сборка, ссылки на приватную часть).
#
# Снимок МЕНЯЕТСЯ (go.mod, go.sum) — так и задумано: публикуется именно он.
# Выход не 0 — снимок публиковать нельзя.
set -euo pipefail

DIR=${1:?каталог снимка}
cd "$DIR"
MOD=$(go list -m)
TOOL=github.com/niazlv/kinopub-downloader

say()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
ok()   { printf '  \033[32mok\033[0m   %s\n' "$1"; }
warn() { printf '  \033[33mwarn\033[0m %s\n' "$1"; }
die()  { printf '  \033[31mFAIL\033[0m %s\n' "$1" >&2; exit 1; }

say "Без площадок"
[[ ! -e providers ]] || die "в снимке есть providers/"
if compgen -G "cmd/*/providers.go" >/dev/null; then die "в снимке есть cmd/*/providers.go"; fi
if leak=$(grep -rln --include='*.go' "\"$MOD/providers" . 2>/dev/null) && [[ -n "$leak" ]]; then
	die "код ссылается на providers/: $(echo $leak)"
fi
ok "providers/ и cmd/*/providers.go нет, импортов на них нет"

say "go.mod без kinopub-downloader"
if grep -q "$TOOL" go.mod; then
	go mod edit -droprequire="$TOOL" -dropreplace="$TOOL"
	echo "  убран require и replace $TOOL"
fi
go mod tidy
! grep -q "$TOOL" go.mod go.sum || die "после tidy в go.mod/go.sum остался $TOOL — его импортирует ядро"
ok "go.mod самостоятельный"

say "Сборка и тесты"
go build ./...
ok "go build ./..."
go vet ./...
ok "go vet ./..."
go test -count=1 ./... >/tmp/kino-snapshot-test.$$ 2>&1 || { tail -40 /tmp/kino-snapshot-test.$$; rm -f /tmp/kino-snapshot-test.$$; die "go test ./..."; }
rm -f /tmp/kino-snapshot-test.$$
ok "go test ./..."
if [[ -x scripts/check-boundaries.sh ]]; then
	./scripts/check-boundaries.sh >/dev/null || { ./scripts/check-boundaries.sh; die "make boundaries"; }
	ok "make boundaries"
fi

say "Что мешает человеку со снимком (не ломает сборку Go)"
quiet=1
if grep -q kinopub-downloader deploy/Dockerfile 2>/dev/null; then
	warn "deploy/Dockerfile копирует соседний kinopub-downloader и собирает из родительского каталога — docker compose build со снимком не пройдёт"
	quiet=0
fi
if grep -rqs "$MOD/providers/" deploy web/apps/android/tools 2>/dev/null; then
	warn "сборочные скрипты передают -X в пакеты providers/ (токен площадки) — в снимке это пустая подстановка"
	quiet=0
fi
[[ $quiet == 0 ]] || ok "ничего"
echo
echo "Снимок собирается и проходит тесты."
