-- +goose Up
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    login         TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('user','admin')),
    disabled      BOOLEAN NOT NULL DEFAULT false,
    totp_secret   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ
);

-- Отдельной таблицей, а не колонкой в users: обычный запрос и админские
-- экраны физически не поднимают хеш пароля.
CREATE TABLE credentials (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    hash    TEXT   NOT NULL
);

CREATE TABLE invites (
    code       TEXT PRIMARY KEY,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_by    BIGINT REFERENCES users(id) ON DELETE SET NULL,
    used_at    TIMESTAMPTZ
);
CREATE INDEX ON invites (used_by) WHERE used_by IS NULL;

-- Хранится ХЕШ токена: утечка дампа БД не должна давать чужие сессии.
CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,
    token_hash  TEXT NOT NULL UNIQUE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space       TEXT   NOT NULL CHECK (space IN ('user','admin','service')),
    device_kind TEXT,
    issued_at   TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX ON sessions (user_id) WHERE revoked_at IS NULL;

ALTER TABLE watch_progress
    ADD CONSTRAINT watch_progress_user_fk
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE playback_sessions
    ADD CONSTRAINT playback_sessions_user_fk
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE admin_audit
    ADD CONSTRAINT admin_audit_actor_fk
    FOREIGN KEY (actor_id) REFERENCES users(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE admin_audit DROP CONSTRAINT IF EXISTS admin_audit_actor_fk;
ALTER TABLE playback_sessions DROP CONSTRAINT IF EXISTS playback_sessions_user_fk;
ALTER TABLE watch_progress DROP CONSTRAINT IF EXISTS watch_progress_user_fk;
DROP TABLE IF EXISTS sessions, invites, credentials, users;
