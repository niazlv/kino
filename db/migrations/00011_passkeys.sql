-- +goose Up
-- Passkey (WebAuthn) как второй фактор и как самостоятельный вход в
-- админку: ключ на устройстве с биометрией или PIN — это уже два фактора,
-- поэтому вход по нему выдаёт повышенную сессию сразу.
--
-- Хранится открытый ключ и счётчик подписей; закрытый ключ не покидает
-- устройство. Ключ привязан к домену (RP ID): на другом домене и через
-- туннель по localhost он не работает — там остаётся пароль + код.
CREATE TABLE webauthn_credentials (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id   BYTEA  NOT NULL UNIQUE,
    public_key      BYTEA  NOT NULL,
    aaguid          BYTEA,
    sign_count      BIGINT NOT NULL DEFAULT 0,
    transports      TEXT[],
    backup_eligible BOOLEAN NOT NULL DEFAULT false,
    backed_up       BOOLEAN NOT NULL DEFAULT false,
    name            TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ
);
CREATE INDEX ON webauthn_credentials (user_id);

-- +goose Down
DROP TABLE IF EXISTS webauthn_credentials;
