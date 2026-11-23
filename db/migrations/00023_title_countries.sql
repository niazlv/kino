-- +goose Up
-- Страны производства. Площадки отдают их вместе с жанрами (kinopub —
-- списком, балансер Alloha — строкой через запятую), и вид произведения
-- без них не определить: «дорама» — это корейский сериал или фильм, а не
-- жанр, которого у площадок нет.
ALTER TABLE canonical_titles ADD COLUMN countries TEXT[];

-- +goose Down
ALTER TABLE canonical_titles DROP COLUMN IF EXISTS countries;
