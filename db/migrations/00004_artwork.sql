-- +goose Up
-- Постер и описание нужны витрине: сетка каталога без постеров нечитаема.
-- Хранится URL у площадки, а не сам файл — платформа его проксирует и кеширует.
ALTER TABLE canonical_titles
    ADD COLUMN poster_url TEXT,
    ADD COLUMN plot       TEXT,
    ADD COLUMN genres     TEXT[],
    ADD COLUMN finished   BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE canonical_titles
    DROP COLUMN IF EXISTS poster_url,
    DROP COLUMN IF EXISTS plot,
    DROP COLUMN IF EXISTS genres,
    DROP COLUMN IF EXISTS finished;
