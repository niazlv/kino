-- +goose Up
-- Даты со стороны площадки.
--
-- «Новинки» должны означать «недавно появилось», а не «недавно обойдено»:
-- порядок обхода зависит от нас и к содержимому отношения не имеет.
ALTER TABLE canonical_titles
    ADD COLUMN source_created_at TIMESTAMPTZ,
    ADD COLUMN source_updated_at TIMESTAMPTZ;

CREATE INDEX ON canonical_titles (source_created_at DESC NULLS LAST);

-- +goose Down
ALTER TABLE canonical_titles
    DROP COLUMN IF EXISTS source_created_at,
    DROP COLUMN IF EXISTS source_updated_at;
