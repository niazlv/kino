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
#   4. проверяет Docker-сборку снимка так, как её увидит человек без .env и
#      без соседних каталогов: куда смотрит контекст (docker compose config)
#      и есть ли в нём всё, что копируют Dockerfile. Это предупреждение, не
#      провал: сборка Go от него не зависит. Нет docker — шаг пропускается.
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

say "Docker-сборка снимка (предупреждения, не провал)"
# Смотрим не на строки в файлах, а на то, что соберёт compose у человека со
# снимком: без .env, без KINO_* в окружении, без соседних каталогов.
# Сервисам нужен .env (env_file): человек делает его из .env.example — так же
# и здесь, только на время разбора, и потом убираем.
made_env=0
if [[ ! -e .env && -f .env.example ]]; then
	cp .env.example .env
	made_env=1
fi
cfg_ok=1
if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
	cfg_ok=0
	warn "нет docker compose — сборку образов не проверял"
elif ! cfg=$(env -u KINO_BUILD_CONTEXT -u KINO_SRC docker compose --project-directory . -f docker-compose.yml \
	config --format json 2>&1); then
	cfg_ok=0
	warn "docker compose config не разобрал docker-compose.yml: $(echo "$cfg" | tail -1)"
fi
[[ $made_env == 0 ]] || rm -f .env
if [[ $cfg_ok == 1 ]]; then
	# Для каждого образа: контекст — сам снимок, Dockerfile в нём есть, и всё,
	# что Dockerfile копирует (кроме шаблонов вида kinopub-download[e]r — им
	# можно ничего не найти), лежит в контексте. ${SRC} — из аргументов
	# сборки compose (по умолчанию «.»).
	issues=$(printf '%s' "$cfg" | python3 -I -c '
import json, os, re, sys
root = os.path.realpath(sys.argv[1])
cfg = json.load(sys.stdin)
seen = set()
for name, svc in sorted((cfg.get("services") or {}).items()):
    b = svc.get("build")
    if not b:
        continue
    ctx = os.path.realpath(b.get("context", "."))
    if ctx != root:
        print(f"{name}: контекст сборки {ctx} — вне снимка (ждём сам снимок)")
        continue
    df = os.path.join(ctx, b.get("dockerfile", "Dockerfile"))
    if not os.path.isfile(df):
        print(f"{name}: нет Dockerfile {os.path.relpath(df, root)}")
        continue
    args = {k: (v if v is not None else "") for k, v in (b.get("args") or {}).items()}
    for line in open(df, encoding="utf-8"):
        m = re.match(r"\s*(?:COPY|ADD)\s+(.*)", line)
        if not m:
            continue
        parts = [p for p in m.group(1).split() if not p.startswith("--")]
        if "--from=" in m.group(1) or len(parts) < 2:
            continue
        for src in parts[:-1]:
            src = re.sub(r"\$\{(\w+)\}", lambda a: args.get(a.group(1), "."), src)
            if re.search(r"[\[*?]", src):
                continue
            key = (os.path.relpath(df, root), src)
            if key in seen:
                continue
            seen.add(key)
            if not os.path.exists(os.path.join(ctx, src)):
                print(f"{name}: {key[0]} копирует {src} — в снимке его нет")
' "$PWD")
	if [[ -n "$issues" ]]; then
		while IFS= read -r l; do warn "$l"; done <<<"$issues"
		warn "docker compose build со снимком не пройдёт"
	else
		ok "контекст — сам снимок, всё, что копируют Dockerfile, в нём есть"
	fi
fi
echo
echo "Снимок собирается и проходит тесты."
