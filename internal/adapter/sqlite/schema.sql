-- Схема движка — подмножество продовой (db/migrations), ровно те таблицы и
-- колонки, что нужны пользовательскому пути: ленивый каталог + состояние
-- одного пользователя. Решения о схеме — ADR-0015.
--
-- Отличия от продовой осознанные:
--   * время — INTEGER (мс от эпохи): время ставит код, читается без догадок
--     о формате;
--   * text[] (genres, countries) — JSON-текст: массивы читает код, нечёткий
--     поиск в SQL не нужен;
--   * title_lower — название в нижнем регистре, сведённое КОДОМ: lower()
--     в SQLite знает только ASCII, а история ищется и по кириллице;
--   * user_titles.rating отсутствует: оценки убраны как концепт, движок
--     стартует с чистого листа.

CREATE TABLE IF NOT EXISTS canonical_titles (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT NOT NULL CHECK (kind IN ('movie','series')),
    imdb_id     TEXT UNIQUE,
    tmdb_id     TEXT,
    kp_id       TEXT,
    title       TEXT NOT NULL,
    title_norm  TEXT NOT NULL,
    title_lower TEXT NOT NULL,
    title_original TEXT,
    year        INTEGER,
    poster_url  TEXT,
    plot        TEXT,
    -- NULL = «площадка не сказала»: пустое не затирает уже известное
    genres      TEXT,
    countries   TEXT,
    finished    INTEGER NOT NULL DEFAULT 0,
    source_created_at INTEGER,
    source_updated_at INTEGER,
    popularity  INTEGER NOT NULL DEFAULT 0,
    sources_checked_at INTEGER
);

CREATE TABLE IF NOT EXISTS external_refs (
    provider_id  TEXT NOT NULL,
    provider_ref TEXT NOT NULL,
    title_id     INTEGER NOT NULL REFERENCES canonical_titles(id) ON DELETE CASCADE,
    priority     INTEGER NOT NULL DEFAULT 0,
    priority_manual INTEGER NOT NULL DEFAULT 0,
    season_no    INTEGER NOT NULL DEFAULT 0,
    mal_id       TEXT,
    matched_by   TEXT,
    disabled     INTEGER NOT NULL DEFAULT 0,
    pinned       INTEGER NOT NULL DEFAULT 0,
    raw          TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (provider_id, provider_ref)
);
CREATE INDEX IF NOT EXISTS idx_refs_title ON external_refs (title_id);
CREATE INDEX IF NOT EXISTS idx_refs_mal ON external_refs (mal_id) WHERE mal_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS canonical_episodes (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    title_id     INTEGER NOT NULL REFERENCES canonical_titles(id) ON DELETE CASCADE,
    season_no    INTEGER NOT NULL,
    episode_no   INTEGER NOT NULL,
    absolute_no  INTEGER,
    duration_sec INTEGER,
    thumbnail_url TEXT,
    title        TEXT,
    UNIQUE (title_id, season_no, episode_no)
);

CREATE TABLE IF NOT EXISTS episode_offers (
    episode_id   INTEGER NOT NULL REFERENCES canonical_episodes(id) ON DELETE CASCADE,
    provider_id  TEXT NOT NULL,
    provider_ref TEXT NOT NULL,
    season_no    INTEGER NOT NULL,
    episode_no   INTEGER NOT NULL,
    seen_at      INTEGER NOT NULL,
    PRIMARY KEY (episode_id, provider_id, provider_ref),
    FOREIGN KEY (provider_id, provider_ref) REFERENCES external_refs (provider_id, provider_ref) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_offers_ref ON episode_offers (provider_id, provider_ref);

CREATE TABLE IF NOT EXISTS title_names (
    title_id INTEGER NOT NULL REFERENCES canonical_titles(id) ON DELETE CASCADE,
    name_key TEXT NOT NULL,
    PRIMARY KEY (title_id, name_key)
);
CREATE INDEX IF NOT EXISTS idx_names_key ON title_names (name_key);

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    login         TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('user','admin')),
    disabled      INTEGER NOT NULL DEFAULT 0,
    totp_secret   TEXT,
    created_at    INTEGER NOT NULL,
    last_login_at INTEGER
);

CREATE TABLE IF NOT EXISTS credentials (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    hash    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS invites (
    code       TEXT PRIMARY KEY,
    created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    used_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    used_at    INTEGER
);

CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    token_hash  TEXT NOT NULL UNIQUE,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space       TEXT NOT NULL CHECK (space IN ('user','admin','service')),
    device_kind TEXT,
    factor_at   INTEGER,
    issued_at   INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    revoked_at  INTEGER,
    refresh_hash TEXT UNIQUE,
    refresh_expires_at INTEGER
);

CREATE TABLE IF NOT EXISTS user_titles (
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title_id  INTEGER NOT NULL REFERENCES canonical_titles(id) ON DELETE CASCADE,
    following INTEGER NOT NULL DEFAULT 0,
    watchlist INTEGER NOT NULL DEFAULT 0,
    hidden    INTEGER NOT NULL DEFAULT 0,
    audio_pref  TEXT,
    text_pref   TEXT,
    source_pref TEXT,
    updated_at  INTEGER NOT NULL,
    unfollowed_at INTEGER,
    PRIMARY KEY (user_id, title_id)
);

CREATE TABLE IF NOT EXISTS watch_progress (
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    episode_id  INTEGER NOT NULL REFERENCES canonical_episodes(id) ON DELETE CASCADE,
    position_ms INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    status      TEXT NOT NULL,
    updated_at  INTEGER NOT NULL,
    client_ts   INTEGER NOT NULL,
    device_id   TEXT,
    PRIMARY KEY (user_id, episode_id)
);

CREATE TABLE IF NOT EXISTS search_history (
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    query       TEXT NOT NULL,
    searched_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, query)
);

CREATE TABLE IF NOT EXISTS share_links (
    token       TEXT PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('watch','download')),
    episode_id  INTEGER NOT NULL REFERENCES canonical_episodes(id) ON DELETE CASCADE,
    created_by  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    selection   TEXT,
    expires_at  INTEGER NOT NULL,
    revoked_at  INTEGER,
    hits        INTEGER NOT NULL DEFAULT 0,
    last_hit_at INTEGER,
    created_at  INTEGER NOT NULL
);

-- Какие запросы уже спрашивали у площадок (LazyCatalog.WithSearchMemory): то,
-- что спрашивали, помнится на диске и переживает перезапуск, чтобы не ходить на
-- чужие сайты дважды за одним и тем же. Ключ — нормализованный запрос.
CREATE TABLE IF NOT EXISTS search_queries (
    query_key TEXT PRIMARY KEY,
    asked_at  INTEGER NOT NULL,
    -- сколько нашли: пустота помнится короче находок
    hits      INTEGER NOT NULL DEFAULT 1
);
