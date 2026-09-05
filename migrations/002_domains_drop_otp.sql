-- Move OTPs out of Postgres. Domain login flags live in `domains`.
DROP TABLE IF EXISTS otp_sessions;

CREATE TABLE IF NOT EXISTS domains (
    domain               VARCHAR(255) PRIMARY KEY,
    email_login_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    sms_login_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO domains (domain, email_login_enabled, sms_login_enabled)
SELECT DISTINCT domain, TRUE, TRUE FROM users
ON CONFLICT (domain) DO NOTHING;

INSERT INTO domains (domain, email_login_enabled, sms_login_enabled)
VALUES ('techhr.com', TRUE, TRUE)
ON CONFLICT (domain) DO NOTHING;
