-- +goose Up
-- «Пересканировать сейчас» из админки: флаг, который обходчик съедает на
-- ближайшем проходе и идёт полным обходом. В базе, а не в памяти, чтобы
-- заявка пережила перезапуск core (админка и обходчик — разные процессы).
ALTER TABLE provider_sync_state
    ADD COLUMN rescan_requested BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE provider_sync_state
    DROP COLUMN IF EXISTS rescan_requested;
