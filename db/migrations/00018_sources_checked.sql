-- +goose Up
-- Когда у аниме-площадок в последний раз искали этот тайтл (досбор при
-- открытии страницы). В базе, а не в памяти: иначе каждый перезапуск core
-- заново искал бы каждый открываемый тайтл, и страница ждала бы площадки,
-- у которых его заведомо нет.
ALTER TABLE canonical_titles ADD COLUMN sources_checked_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE canonical_titles DROP COLUMN IF EXISTS sources_checked_at;
