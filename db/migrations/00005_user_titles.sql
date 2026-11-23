-- +goose Up
-- Намеренные отметки пользователя о тайтле.
--
-- Отдельно от watch_progress намеренно: позиция просмотра ВЫВОДИТСЯ из того,
-- что человек включил, а «слежу» и «буду смотреть» — это его решение.
-- Смешивать их значит объявлять слежением случайно открытую серию.
--
-- Одна строка на (пользователь, тайтл) вместо таблицы на каждую отметку:
-- отметки редко живут поодиночке, а запросы к ним почти всегда про один
-- и тот же тайтл.
CREATE TABLE user_titles (
    user_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title_id  BIGINT NOT NULL REFERENCES canonical_titles(id) ON DELETE CASCADE,

    -- Слежу: новые серии этого сериала мне интересны.
    following BOOLEAN NOT NULL DEFAULT false,
    -- Буду смотреть: отложено на потом.
    watchlist BOOLEAN NOT NULL DEFAULT false,
    -- Оценка 1..10; NULL — не оценивал. Ноль и «не оценивал» разные вещи.
    rating    SMALLINT CHECK (rating BETWEEN 1 AND 10),
    -- Скрыт: не показывать в подборках. Право не видеть — тоже решение.
    hidden    BOOLEAN NOT NULL DEFAULT false,

    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, title_id)
);

-- Частичные индексы: выборки идут по одной отметке за раз, и хранить
-- в индексе строки без неё незачем.
CREATE INDEX ON user_titles (user_id, updated_at DESC) WHERE following;
CREATE INDEX ON user_titles (user_id, updated_at DESC) WHERE watchlist;

-- +goose Down
DROP TABLE IF EXISTS user_titles;
