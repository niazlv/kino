-- +goose Up
-- Курсор инкрементального обхода на провайдера. Без него каждый запуск
-- кравлера тянул бы каталог целиком: 53 тысячи элементов вместо десятка
-- изменившихся.
CREATE TABLE provider_sync_state (
    provider_id   TEXT PRIMARY KEY,
    last_synced_at TIMESTAMPTZ,
    last_run_at    TIMESTAMPTZ,
    last_error     TEXT,
    titles_seen    BIGINT NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE IF EXISTS provider_sync_state;
