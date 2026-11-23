-- +goose Up
-- Двухшаговый вход в админку: пароль выдаёт ЧАСТИЧНУЮ сессию, код её
-- повышает. Отметка времени, а не флаг: опасные действия требуют кода,
-- введённого недавно (sudo-режим), и без времени этого не проверить.
ALTER TABLE sessions ADD COLUMN factor_at TIMESTAMPTZ;

-- Живые админские сессии, выданные до этого шага, прошли пароль и код одной
-- формой: считать их повышенными честно, и никого не выкидывает.
UPDATE sessions SET factor_at = issued_at WHERE space = 'admin' AND revoked_at IS NULL;

-- +goose Down
ALTER TABLE sessions DROP COLUMN IF EXISTS factor_at;
