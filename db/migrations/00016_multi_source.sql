-- +goose Up
-- Несколько площадок у одного тайтла.
--
-- До этого у каждого тайтла была ровно одна площадка, и номер серии у неё
-- совпадал с каноническим. Аниме-площадки устроены иначе: сезон — это
-- отдельная страница с сериями от единицы, озвучки — отдельные потоки, и
-- одни и те же серии есть сразу у нескольких площадок. Ниже — ровно то,
-- что нужно, чтобы площадки ДОПОЛНЯЛИ друг друга, а не заводили дубли.

-- Оригинальное название отдельно от витринного: склейке аниме оно нужнее
-- русского, а в витринном у kinopub оно вклеено через « / ».
ALTER TABLE canonical_titles ADD COLUMN title_original TEXT;

-- Все известные написания тайтла в нормализованном виде. Ключи строит код
-- (app/matching), а не SQL: одна и та же нормализация должна давать один
-- и тот же ключ при записи и при поиске, и держать её в двух языках —
-- значит однажды получить расхождение и молча не склеить.
CREATE TABLE title_names (
    title_id BIGINT NOT NULL REFERENCES canonical_titles(id) ON DELETE CASCADE,
    name_key TEXT   NOT NULL,
    PRIMARY KEY (title_id, name_key)
);
CREATE INDEX ON title_names (name_key);

ALTER TABLE external_refs
    -- > 0 — элемент площадки покрывает только этот сезон произведения.
    ADD COLUMN season_no INT NOT NULL DEFAULT 0,
    -- Выключен вручную: не резолвится, но помнится, иначе обход завёл бы
    -- связь заново.
    ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT false,
    -- Номер MyAnimeList/Shikimori. Сезонный, поэтому здесь, а не у тайтла.
    ADD COLUMN mal_id TEXT,
    -- Чем склеено: ref | imdb | kp | mal | name | new | manual. Нужно
    -- админке, чтобы сомнительные склейки было видно.
    ADD COLUMN matched_by TEXT,
    -- Привязку поставил человек: обход её не перезаписывает.
    ADD COLUMN pinned BOOLEAN NOT NULL DEFAULT false,
    -- Приоритет задан вручную. Иначе он берётся из конфига площадки при
    -- чтении — и смена конфига сразу действует на весь каталог.
    ADD COLUMN priority_manual BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX ON external_refs (mal_id) WHERE mal_id IS NOT NULL;
CREATE INDEX ON external_refs (title_id);

-- Какая площадка какую серию отдаёт и под каким номером У СЕБЯ.
--
-- Пересчёт номеров делается один раз, при записи серий, и хранится:
-- резолв спрашивает площадку её же номерами. Здесь же видно, у кого серии
-- нет, — и плеер идёт к тому, у кого она есть.
CREATE TABLE episode_offers (
    episode_id   BIGINT NOT NULL REFERENCES canonical_episodes(id) ON DELETE CASCADE,
    provider_id  TEXT   NOT NULL,
    provider_ref TEXT   NOT NULL,
    season_no    INT    NOT NULL,
    episode_no   INT    NOT NULL,
    seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (episode_id, provider_id, provider_ref),
    FOREIGN KEY (provider_id, provider_ref)
        REFERENCES external_refs (provider_id, provider_ref) ON DELETE CASCADE
);
CREATE INDEX ON episode_offers (provider_id, provider_ref);

-- Всё, что заведено до сих пор, пришло с одной площадки в её же нумерации.
INSERT INTO episode_offers (episode_id, provider_id, provider_ref, season_no, episode_no)
SELECT e.id, x.provider_id, x.provider_ref, e.season_no, e.episode_no
FROM canonical_episodes e
JOIN external_refs x ON x.title_id = e.title_id;

-- Площадка, выбранная человеком для тайтла. Рядом с audio_pref: озвучка у
-- аниме-площадок и есть выбор площадки и потока.
ALTER TABLE user_titles ADD COLUMN source_pref TEXT;

-- +goose Down
ALTER TABLE user_titles DROP COLUMN IF EXISTS source_pref;
DROP TABLE IF EXISTS episode_offers;
DROP INDEX IF EXISTS external_refs_title_id_idx;
DROP INDEX IF EXISTS external_refs_mal_id_idx;
ALTER TABLE external_refs
    DROP COLUMN IF EXISTS season_no,
    DROP COLUMN IF EXISTS disabled,
    DROP COLUMN IF EXISTS mal_id,
    DROP COLUMN IF EXISTS matched_by,
    DROP COLUMN IF EXISTS pinned,
    DROP COLUMN IF EXISTS priority_manual;
DROP TABLE IF EXISTS title_names;
ALTER TABLE canonical_titles DROP COLUMN IF EXISTS title_original;
