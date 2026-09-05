CREATE TABLE IF NOT EXISTS oauth_tokens (
    id          UUID PRIMARY KEY,
    jti         VARCHAR(64)  NOT NULL UNIQUE,
    family_id   VARCHAR(64)  NOT NULL,
    domain      VARCHAR(255) NOT NULL,
    type        VARCHAR(16)  NOT NULL CHECK (type IN ('email', 'sms')),
    identifier  VARCHAR(255) NOT NULL,
    kind        VARCHAR(16)  NOT NULL CHECK (kind IN ('access', 'refresh')),
    token_hash  TEXT         NOT NULL,
    expires_at  TIMESTAMPTZ  NOT NULL,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_tokens_jti_active
    ON oauth_tokens (jti)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_oauth_tokens_user_active
    ON oauth_tokens (domain, type, identifier, kind)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_oauth_tokens_family
    ON oauth_tokens (family_id);
