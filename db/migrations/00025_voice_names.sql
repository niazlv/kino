-- +goose Up
-- Одна озвучка у площадок пишется по-разному: «DUB» и «Дублированный»,
-- «AniLibria» и «AniLiberty», «Мега-Аниме» и «Mega-Anime». Вид озвучки без
-- студии сводит разбор подписи в коде; студии под разными именами — эта
-- таблица (app/voicenames). Пополняют её обучение по транслиту и человек в
-- админке. Номер 00025: 00023 занят невлитой работой со странами.

-- Как площадки пишут озвучки: из этого обучение находит псевдонимы, а
-- админка — похожие написания. key — ключ одной подписи, без псевдонимов.
CREATE TABLE voice_spellings (
    key         TEXT        NOT NULL,
    title       TEXT        NOT NULL,
    provider_id TEXT        NOT NULL,
    -- Где встретилась последней: человеку проверить на слух. Без внешнего
    -- ключа — серии склеиваются и пересобираются, а пример не повод мешать.
    episode_id  BIGINT,
    hits        INTEGER     NOT NULL DEFAULT 1,
    first_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (key, title, provider_id)
);

-- Написание from_key — это студия to_key. Цепочек нет: кто указывал на
-- склеенную студию, переуказывается на её главную.
CREATE TABLE voice_aliases (
    from_key   TEXT        PRIMARY KEY,
    to_key     TEXT        NOT NULL CHECK (to_key <> from_key),
    origin     TEXT        NOT NULL CHECK (origin IN ('admin', 'auto')),
    note       TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Пары, которые человек назвал разными: обучение их не склеивает, админка
-- не предлагает. Пара хранится упорядоченной.
CREATE TABLE voice_alias_rejects (
    a          TEXT        NOT NULL,
    b          TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (a, b),
    CHECK (a < b)
);

-- Известные переименования, которые транслит не находит.
INSERT INTO voice_aliases (from_key, to_key, origin, note) VALUES
    ('aniliberty', 'anilibria', 'admin', 'AniLibria в 2025-м переименовалась в AniLiberty'),
    ('студийнаябанда', 'studioband', 'admin', '«Студийная Банда» — русское имя Studio Band');

-- +goose Down
DROP TABLE IF EXISTS voice_alias_rejects;
DROP TABLE IF EXISTS voice_aliases;
DROP TABLE IF EXISTS voice_spellings;
