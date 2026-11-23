# Контекст сборки — один из двух, путь проекта в нём — аргумент SRC:
#   - сам репозиторий, SRC=. — публичная сборка ядра (без площадок), ей
#     соседи не нужны; так собирает `docker compose build` по умолчанию;
#   - родительский каталог, SRC=<имя каталога проекта> — полная сборка с
#     площадками: go.mod ссылается на соседний ../kinopub-downloader через
#     replace, а docker не видит ничего выше своего контекста. Так собирают
#     `make up` (сам видит replace в go.mod) и deploy.sh: KINO_BUILD_CONTEXT=..
#     и KINO_SRC=<каталог> в окружении compose.
#
# 1.27: kinopub-downloader заявляет go 1.27.1 — тулчейн 1.26 его модуль не
# собирает («requires go >= 1.27.1»), образ идёт в ногу.
ARG CMD
FROM golang:1.27-alpine AS build
ARG SRC=.
# Утилита — только если она есть в контексте: шаблон [e] совпадает с
# каталогом или ни с чем, а LICENSE.md проекта держит COPY непустым.
COPY ${SRC}/LICENSE.md kinopub-download[e]r /src/kinopub-downloader/
WORKDIR /src/kino
COPY ${SRC}/go.mod ${SRC}/go.sum ./
RUN go mod download || true
COPY ${SRC} ./

ARG CMD
# Запасной токен партнёра балансера для площадки lordfilm — чужой, поэтому в
# исходниках его нет; приходит аргументом сборки (compose берёт его из
# окружения, deploy.sh — из deploy/prod/local.env). Пусто — площадка возьмёт
# токен со страницы сайта.
ARG LORDFILM_ALLOHA_DEFAULT_TOKEN=""
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-X github.com/niazleushkin/kino/providers/lordfilm.defaultToken=${LORDFILM_ALLOHA_DEFAULT_TOKEN}" \
    -o /out/app ./cmd/${CMD}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates ffmpeg tzdata
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
