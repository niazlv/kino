-- +goose Up
-- Кадр из серии. Список серий одними номерами читается заметно хуже.
-- Хранится URL у площадки: платформа его проксирует, как и постеры.
ALTER TABLE canonical_episodes ADD COLUMN thumbnail_url TEXT;

-- +goose Down
ALTER TABLE canonical_episodes DROP COLUMN IF EXISTS thumbnail_url;
