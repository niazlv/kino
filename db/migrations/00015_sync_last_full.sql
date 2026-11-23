-- +goose Up
-- Когда каталог в последний раз обходили целиком. Полный обход — пятьдесят
-- тысяч запросов к площадке, и он должен случаться редко и ночью; между
-- ними синхронизация идёт по курсору last_synced_at. Отметка нужна в базе,
-- а не в памяти: иначе каждый перезапуск обходчика начинал бы с полного
-- прохода, а перезапуск — это каждый деплой.
ALTER TABLE provider_sync_state
    ADD COLUMN last_full_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE provider_sync_state
    DROP COLUMN IF EXISTS last_full_at;
