-- +goose Up
-- История поиска: своя у каждого и живёт на сервере, как прогресс и
-- отметки (ADR-0005) — телефон, браузер и телевизор видят один список,
-- а на телевизоре набирать запрос каждый раз мучительно. Ключ по паре
-- (пользователь, запрос): повторный поиск поднимает запрос наверх,
-- а не плодит дубли.
CREATE TABLE search_history (
    user_id     BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    query       TEXT        NOT NULL,
    searched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, query)
);

-- +goose Down
DROP TABLE IF EXISTS search_history;
