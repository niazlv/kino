-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Каноническая идентичность: пользовательское состояние ключуется ТОЛЬКО сюда.
CREATE TABLE canonical_titles (
    id          BIGSERIAL PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('movie','series')),
    imdb_id     TEXT,
    tmdb_id     TEXT,
    kp_id       TEXT,
    title       TEXT NOT NULL,
    title_norm  TEXT NOT NULL,
    year        INT,
    schema_version INT NOT NULL DEFAULT 1   -- ось версионирования 2
);
CREATE UNIQUE INDEX ON canonical_titles (imdb_id) WHERE imdb_id IS NOT NULL;
CREATE INDEX ON canonical_titles USING gin (title_norm gin_trgm_ops);

-- Присутствие тайтла на площадке. Many-to-one: один канонический тайтл
-- может приходить из нескольких источников.
CREATE TABLE external_refs (
    provider_id  TEXT   NOT NULL,
    provider_ref TEXT   NOT NULL,
    title_id     BIGINT NOT NULL REFERENCES canonical_titles(id) ON DELETE CASCADE,
    priority     INT    NOT NULL DEFAULT 0,
    raw          JSONB  NOT NULL DEFAULT '{}',  -- страховка для backfill'а
    PRIMARY KEY (provider_id, provider_ref)
);

CREATE TABLE canonical_episodes (
    id          BIGSERIAL PRIMARY KEY,
    title_id    BIGINT NOT NULL REFERENCES canonical_titles(id) ON DELETE CASCADE,
    season_no   INT NOT NULL,
    episode_no  INT NOT NULL,
    absolute_no INT,                      -- площадки расходятся в нумерации
    duration_sec INT,
    UNIQUE (title_id, season_no, episode_no)
);

-- ФУНКЦИОНАЛЬНЫЕ данные, не аналитика. Админского экрана над этим быть
-- не должно — см. ADR-0005.
CREATE TABLE watch_progress (
    user_id     BIGINT NOT NULL,
    episode_id  BIGINT NOT NULL REFERENCES canonical_episodes(id) ON DELETE CASCADE,
    position_ms BIGINT NOT NULL,
    duration_ms BIGINT NOT NULL,
    status      TEXT   NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    client_ts   TIMESTAMPTZ NOT NULL,
    device_id   TEXT,
    PRIMARY KEY (user_id, episode_id)
);

-- Одна строка на ПРОСМОТР, а не на запрос (ADR-0005). IP намеренно отсутствует.
CREATE TABLE playback_sessions (
    id           TEXT PRIMARY KEY,
    user_id      BIGINT NOT NULL,
    episode_id   BIGINT REFERENCES canonical_episodes(id) ON DELETE SET NULL,
    provider_id  TEXT,
    started_at   TIMESTAMPTZ NOT NULL,
    ended_at     TIMESTAMPTZ,
    device_kind  TEXT,
    app_version  TEXT,
    player_impl  TEXT,   -- hlsjs | native_hls: без этого «на айфоне тормозит» неотлаживаемо
    delivery     TEXT,   -- proxy | redirect | local
    ttff_ms      BIGINT,
    rebuffer_count INT NOT NULL DEFAULT 0,
    rebuffer_ms_total BIGINT NOT NULL DEFAULT 0,
    bitrate_switches  INT NOT NULL DEFAULT 0,
    max_height   INT,
    bytes_served BIGINT NOT NULL DEFAULT 0,
    segments_served BIGINT NOT NULL DEFAULT 0,
    end_reason   TEXT,
    error_code   TEXT
);
CREATE INDEX ON playback_sessions (started_at);

-- Каждая мутация в админке. Не ради формальностей: ради вопроса
-- «почему у мамы вчера пропал доступ» через месяц.
CREATE TABLE admin_audit (
    id         BIGSERIAL PRIMARY KEY,
    actor_id   BIGINT NOT NULL,
    action     TEXT   NOT NULL,
    target     TEXT,
    before     JSONB,
    after      JSONB,
    reason     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS admin_audit, playback_sessions, watch_progress,
                     canonical_episodes, external_refs, canonical_titles;
