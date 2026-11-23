.PHONY: help generate lint boundaries test up down migrate player-demo tools dev-db dev-core probe kinopub-token backup-install backup-run

DEV_DSN := postgres://kino:kino@localhost:55432/kino?sslmode=disable

# Полная сборка (go.mod ссылается на соседний ../kinopub-downloader) идёт в
# docker из родительского каталога; публичная — из самого репозитория.
ifneq ($(shell grep -c '=> ../kinopub-downloader' go.mod 2>/dev/null),0)
export KINO_BUILD_CONTEXT ?= ..
export KINO_SRC ?= $(notdir $(CURDIR))
endif

help:
	@grep -hE '^[a-z-]+:.*?##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | column -t -s "$$(printf '\t')"

tools: ## поставить генераторы в ./bin
	GOBIN=$(PWD)/bin go install github.com/bufbuild/buf/cmd/buf@latest
	GOBIN=$(PWD)/bin go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	GOBIN=$(PWD)/bin go install github.com/pressly/goose/v3/cmd/goose@latest

generate: ## proto + sqlc + openapi -> код
	./bin/buf generate
	./bin/sqlc generate

lint: ## golangci-lint (включает depguard = правила слоёв)
	golangci-lint run ./...

boundaries: ## жёсткие архитектурные инварианты (см. docs/boundaries.md)
	@./scripts/check-boundaries.sh

test:
	go test ./...

up:   ; docker compose up -d --build
down: ; docker compose down

dev-db: ## одиночный Postgres для разработки на :55432 + миграции
	docker rm -f kino-pg-dev 2>/dev/null || true
	docker run -d --name kino-pg-dev -e POSTGRES_USER=kino -e POSTGRES_PASSWORD=kino \
		-e POSTGRES_DB=kino -p 55432:5432 postgres:16-alpine
	@until docker exec kino-pg-dev pg_isready -U kino >/dev/null 2>&1; do sleep 1; done
	POSTGRES_DSN="$(DEV_DSN)" $(MAKE) migrate

dev-core: ## запустить core локально против dev-db
	POSTGRES_DSN="$(DEV_DSN)" CORE_SECURE_COOKIES=false go run ./cmd/core

kinopub-token: ## вытащить токен kinopub из локального хранилища утилиты в .env
	@test -f .env || cp .env.example .env
	@cd ../kinopub-downloader && go run ./cmd/kinopub sessions export --out /tmp/kp-session.json >/dev/null 2>&1 \
		|| { echo "нет сохранённой сессии; выполните: kinopub login --qr"; exit 1; }
	@python3 scripts/token-into-env.py /tmp/kp-session.json .env
	@rm -f /tmp/kp-session.json
	@chmod 600 .env
	@echo "токен записан в .env (0600); файл в .gitignore"

probe: ## фаза 0: проверить CDN-ссылку (ADR-0003). make probe URL=...
	go run ./cmd/probe -url "$(URL)"

backup-install: ## ежедневный бэкап Postgres на прод: юнит+таймер, пробный дамп
	bash deploy/prod/backup/install.sh

backup-run: ## снять дамп на проде прямо сейчас
	bash -c '. deploy/prod/local-env.sh; : "$${DEPLOY_HOST:?задайте DEPLOY_HOST в deploy/prod/local.env}"; \
		ssh -o BatchMode=yes "$${DEPLOY_USER:-root}@$$DEPLOY_HOST" \
		"systemctl start kino-backup.service && journalctl -t kino-backup -n 5 --no-pager"'

migrate: ## применить миграции
	./bin/goose -dir db/migrations postgres "$$POSTGRES_DSN" up

player-demo: ## плеер отдельно, без бэкенда — доказательство отделяемости
	cd web && pnpm --filter @kino/player demo
