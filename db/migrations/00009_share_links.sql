-- +goose Up
-- Ссылки-разрешения: открываются БЕЗ входа, потому что сам токен и есть
-- право доступа.
--
-- Хранятся, а не подписываются без состояния, ровно ради одного: отзыва.
-- Подписанный токен нельзя отменить до истечения срока, а раздача доступа
-- к платному контенту наружу без возможности передумать — плохая идея.
CREATE TABLE share_links (
    token      TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('watch', 'download')),
    episode_id BIGINT NOT NULL REFERENCES canonical_episodes(id) ON DELETE CASCADE,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Выбор дорожек и качества для скачивания; для watch не используется.
    selection  JSONB,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    -- Учёт обращений: раздал ссылку — стоит видеть, пользуются ли ею.
    hits       INT NOT NULL DEFAULT 0,
    last_hit_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON share_links (created_by, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS share_links;
