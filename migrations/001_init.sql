-- Users: composite primary key (domain, type, identifier)
CREATE TABLE IF NOT EXISTS users (
    domain      VARCHAR(255) NOT NULL,
    type        VARCHAR(16)  NOT NULL CHECK (type IN ('email', 'sms')),
    identifier  VARCHAR(255) NOT NULL,
    password_hash TEXT,
    role        VARCHAR(32)  NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    active      BOOLEAN      NOT NULL DEFAULT TRUE,
    display_name VARCHAR(255),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (domain, type, identifier)
);

CREATE INDEX IF NOT EXISTS idx_users_domain_role ON users (domain, role);

-- Domain-level login methods (email/password and/or mobile OTP)
CREATE TABLE IF NOT EXISTS domains (
    domain               VARCHAR(255) PRIMARY KEY,
    email_login_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    sms_login_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Internal portal app links per domain
CREATE TABLE IF NOT EXISTS portal_apps (
    id          UUID PRIMARY KEY,
    domain      VARCHAR(255) NOT NULL,
    section     VARCHAR(255) NOT NULL DEFAULT 'Applications',
    name        VARCHAR(255) NOT NULL,
    url         TEXT         NOT NULL,
    icon_url    TEXT,
    sort_order  INT          NOT NULL DEFAULT 0,
    active      BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_portal_apps_domain ON portal_apps (domain, active, sort_order);
CREATE UNIQUE INDEX IF NOT EXISTS uq_portal_apps_domain_name ON portal_apps (domain, name);

-- Seed default admin (password: Admin@123)
INSERT INTO users (domain, type, identifier, password_hash, role, display_name)
VALUES (
    'techhr.com',
    'email',
    'admin@techhr.com',
    'PENDING_ARGON2ID',
    'admin',
    'Portal Admin'
) ON CONFLICT (domain, type, identifier) DO NOTHING;

INSERT INTO domains (domain, email_login_enabled, sms_login_enabled)
VALUES ('techhr.com', TRUE, TRUE)
ON CONFLICT (domain) DO NOTHING;

-- Seed sample apps for techhr.com
INSERT INTO portal_apps (id, domain, section, name, url, icon_url, sort_order) VALUES
    ('01a0694c-4ebd-7dda-b2c0-a2b88250d648', 'techhr.com', 'Tech Applications', 'HackerRank', 'https://www.hackerrank.com', 'https://cdn.jsdelivr.net/gh/simple-icons/simple-icons/icons/hackerrank.svg', 1),
    ('01a0694c-4ebd-7e44-96a2-3d7a5fec8928', 'techhr.com', 'Tech Applications', 'UberHub Portal', 'https://hub.uberinternal.com', NULL, 2),
    ('01a0694c-4ebd-7e50-a8bd-f590a6b66a6b', 'techhr.com', 'Tech Applications', 'Blackline Production', 'https://blackline.com', NULL, 3),
    ('01a0694c-4ebd-7e53-9de9-c3b3ef81460f', 'techhr.com', 'Tech Applications', 'SurveyMonkey', 'https://www.surveymonkey.com', NULL, 4),
    ('01a0694c-4ebd-7e57-87e5-53e2d2b763de', 'techhr.com', 'Tech Applications', 'AlertMedia', 'https://www.alertmedia.com', NULL, 5),
    ('01a0694c-4ebd-7e5f-8f63-3a66952a258e', 'techhr.com', 'Tech Applications', 'Fintech go-inventory', 'https://inventory.techhr.com', NULL, 6),
    ('01a0694c-4ebd-7e63-93a4-4114df2c7698', 'techhr.com', 'Tech Applications', 'Hoxhunt', 'https://hoxhunt.com', NULL, 7),
    ('01a0694c-4ebd-7e67-99ff-1a9ef5cc37d9', 'techhr.com', 'Tech Applications', 'Google Vids', 'https://vids.google.com', NULL, 8),
    ('01a0694c-4ebd-7e6b-a9bf-537347842805', 'techhr.com', 'Tech Applications', 'NICE WFM', 'https://nice.com', NULL, 9),
    ('01a0694c-4ebd-7e6f-964a-cf2ce854e862', 'techhr.com', 'Tech Applications', 'tools.uberinternal.co', 'https://tools.uberinternal.co', NULL, 10),
    ('01a0694c-4ebd-7e70-91ff-8186b82e58a5', 'techhr.com', 'Tech Applications', 'Slido (Participants)', 'https://www.slido.com', NULL, 11),
    ('01a0694c-4ebd-7e73-adcb-9caf3c63e38d', 'techhr.com', 'Tech Applications', 'Teamdot', 'https://teamdot.techhr.com', NULL, 12),
    ('01a0694c-4ebd-7e77-add6-a5449128c038', 'techhr.com', 'Tech Applications', 'Uber Data Platform', 'https://data.uberinternal.com', NULL, 13)
ON CONFLICT (domain, name) DO NOTHING;
