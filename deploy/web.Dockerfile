# Сборка фронтенда в статику. Раздаёт её Caddy, а не Node: preview-сервер
# Vite не рассчитан на прокачку видеосегментов и блокирует очередь.
# Контекст и SRC — как у deploy/Dockerfile: сам репозиторий (SRC=.) или
# родительский каталог (SRC=<каталог проекта>).
# Статика от платформы не зависит: собирается на машине сборки.
FROM --platform=$BUILDPLATFORM node:22-alpine AS build
ARG SRC=.
WORKDIR /src
RUN corepack enable
COPY ${SRC}/web/package.json ${SRC}/web/pnpm-workspace.yaml ${SRC}/web/pnpm-lock.yaml ./
COPY ${SRC}/web/packages ./packages
COPY ${SRC}/web/apps ./apps
RUN pnpm install --frozen-lockfile || pnpm install
RUN pnpm --filter @kino/app build

# Результат забирает caddy через общий том.
FROM alpine:3.20
COPY --from=build /src/apps/web/dist /dist
CMD ["sh", "-c", "rm -rf /srv/web/* && cp -r /dist/. /srv/web/ && echo 'фронтенд разложен' && sleep infinity"]
