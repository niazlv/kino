-- +goose Up
-- Суточные агрегаты телеметрии.
--
-- Детали живут 90 дней, агрегаты — вечно: они весят килобайты, а отвечают
-- на те же вопросы «стало ли хуже» и «на каком движке» (ADR-0005).
-- Разбивка по движку плеера обязательна: без неё жалоба «на айфоне
-- тормозит» неотличима от проблемы сети.
CREATE TABLE playback_daily (
    day           DATE   NOT NULL,
    player_impl   TEXT   NOT NULL DEFAULT 'неизвестен',
    sessions      INT    NOT NULL,
    errors        INT    NOT NULL,
    rebuffer_avg  REAL   NOT NULL,
    ttff_p50_ms   BIGINT NOT NULL,
    bytes_served  BIGINT NOT NULL,
    PRIMARY KEY (day, player_impl)
);

-- +goose Down
DROP TABLE IF EXISTS playback_daily;
