-- +goose Up
-- Сессии устройств: утилита-загрузчик входит по коду, который человек
-- подтверждает на сайте своей сессией, и дальше живёт без браузерных куки.
-- Сессия при этом обычная, но с refresh-токеном: им она продлевается сама,
-- пока устройством пользуются. Хранится хешем, как и токен сессии — дамп
-- БД не должен давать возможность предъявить чужую.
ALTER TABLE sessions
    ADD COLUMN refresh_hash       TEXT UNIQUE,
    ADD COLUMN refresh_expires_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE sessions
    DROP COLUMN IF EXISTS refresh_hash,
    DROP COLUMN IF EXISTS refresh_expires_at;
