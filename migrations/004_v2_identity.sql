CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS organizations (
    id UUID PRIMARY KEY,
    slug VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    email_login_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sms_login_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_organizations_slug_lower
    ON organizations (LOWER(slug));

CREATE TABLE IF NOT EXISTS persons (
    id UUID PRIMARY KEY,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS organization_memberships (
    id UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    person_id UUID NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, person_id),
    UNIQUE (person_id)
);
CREATE INDEX IF NOT EXISTS idx_memberships_organization
    ON organization_memberships (organization_id, active);
CREATE INDEX IF NOT EXISTS idx_memberships_organization_created
    ON organization_memberships (organization_id, created_at DESC);

CREATE TABLE IF NOT EXISTS login_identities (
    id UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    person_id UUID NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    type VARCHAR(16) NOT NULL CHECK (type IN ('email', 'sms')),
    identifier VARCHAR(255) NOT NULL,
    password_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_login_identities_global
    ON login_identities (type, LOWER(identifier));
CREATE UNIQUE INDEX IF NOT EXISTS uq_login_identities_org
    ON login_identities (organization_id, type, LOWER(identifier));
CREATE INDEX IF NOT EXISTS idx_login_identities_person
    ON login_identities (person_id);

CREATE TABLE IF NOT EXISTS permissions (
    key VARCHAR(64) PRIMARY KEY,
    description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS organization_roles (
    id UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL,
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_organization_roles_key
    ON organization_roles (organization_id, LOWER(key));

CREATE TABLE IF NOT EXISTS organization_role_permissions (
    role_id UUID NOT NULL REFERENCES organization_roles(id) ON DELETE CASCADE,
    permission_key VARCHAR(64) NOT NULL REFERENCES permissions(key) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_key)
);

CREATE TABLE IF NOT EXISTS membership_roles (
    membership_id UUID NOT NULL REFERENCES organization_memberships(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES organization_roles(id) ON DELETE CASCADE,
    PRIMARY KEY (membership_id, role_id)
);

INSERT INTO permissions (key, description) VALUES
    ('profile:read', 'Read own profile'),
    ('apps:read', 'Read organization applications'),
    ('apps:write', 'Manage organization applications'),
    ('members:read', 'Read organization members'),
    ('members:write', 'Manage organization members'),
    ('roles:read', 'Read organization roles'),
    ('roles:write', 'Manage custom organization roles'),
    ('organization:read', 'Read organization settings'),
    ('organization:write', 'Manage organization settings')
ON CONFLICT (key) DO NOTHING;

-- Backfill organizations. gen_random_uuid is used only for existing rows; all
-- application-created records use internal/id.NewUUIDv7.
INSERT INTO organizations (id, slug, name, email_login_enabled, sms_login_enabled, created_at, updated_at)
SELECT gen_random_uuid(), d.domain, d.domain, d.email_login_enabled, d.sms_login_enabled, d.created_at, d.updated_at
FROM domains d
ON CONFLICT DO NOTHING;

INSERT INTO organization_roles (id, organization_id, key, name, is_system)
SELECT gen_random_uuid(), o.id, seed.key, seed.name, TRUE
FROM organizations o
CROSS JOIN (VALUES
    ('org_admin', 'Org admin'),
    ('member', 'Member'),
    ('project_manager', 'Project Manager'),
    ('contractor', 'Contractor'),
    ('finance', 'Finance')
) AS seed(key, name)
ON CONFLICT DO NOTHING;

INSERT INTO organization_role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM organization_roles r
JOIN permissions p ON
    r.key = 'org_admin'
    OR (r.key IN ('member', 'project_manager', 'contractor', 'finance')
        AND p.key IN ('profile:read', 'apps:read'))
ON CONFLICT DO NOTHING;

-- V1 has no person concept, so each legacy user becomes one person and one
-- membership. The stable md5 inputs make this idempotent under the current
-- migration runner, which reapplies SQL files at startup.
INSERT INTO persons (id, display_name, created_at, updated_at)
SELECT (md5('person:' || u.domain || ':' || u.type || ':' || LOWER(u.identifier)))::uuid,
       COALESCE(u.display_name, ''), u.created_at, u.updated_at
FROM users u
ON CONFLICT (id) DO NOTHING;

INSERT INTO organization_memberships (id, organization_id, person_id, active, created_at, updated_at)
SELECT (md5('membership:' || u.domain || ':' || u.type || ':' || LOWER(u.identifier)))::uuid,
       o.id,
       (md5('person:' || u.domain || ':' || u.type || ':' || LOWER(u.identifier)))::uuid,
       u.active, u.created_at, u.updated_at
FROM users u
JOIN organizations o ON LOWER(o.slug) = LOWER(u.domain)
ON CONFLICT DO NOTHING;

INSERT INTO login_identities (id, organization_id, person_id, type, identifier, password_hash, created_at, updated_at)
SELECT (md5('identity:' || u.type || ':' || LOWER(u.identifier)))::uuid,
       o.id,
       (md5('person:' || u.domain || ':' || u.type || ':' || LOWER(u.identifier)))::uuid,
       u.type, u.identifier, u.password_hash, u.created_at, u.updated_at
FROM users u
JOIN organizations o ON LOWER(o.slug) = LOWER(u.domain)
ON CONFLICT DO NOTHING;

INSERT INTO membership_roles (membership_id, role_id)
SELECT m.id, r.id
FROM users u
JOIN organizations o ON LOWER(o.slug) = LOWER(u.domain)
JOIN organization_memberships m
  ON m.id = (md5('membership:' || u.domain || ':' || u.type || ':' || LOWER(u.identifier)))::uuid
JOIN organization_roles r
  ON r.organization_id = o.id
 AND r.key = CASE WHEN u.role = 'admin' THEN 'org_admin' ELSE 'member' END
ON CONFLICT DO NOTHING;

ALTER TABLE portal_apps ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE portal_apps a
SET organization_id = o.id
FROM organizations o
WHERE a.organization_id IS NULL AND LOWER(o.slug) = LOWER(a.domain);
CREATE INDEX IF NOT EXISTS idx_portal_apps_organization
    ON portal_apps (organization_id, active, sort_order);

ALTER TABLE oauth_tokens ADD COLUMN IF NOT EXISTS membership_id UUID REFERENCES organization_memberships(id);
ALTER TABLE oauth_tokens ADD COLUMN IF NOT EXISTS identity_id UUID REFERENCES login_identities(id);
ALTER TABLE oauth_tokens ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id);
UPDATE oauth_tokens t
SET organization_id = i.organization_id,
    identity_id = i.id,
    membership_id = m.id
FROM login_identities i
JOIN organization_memberships m
  ON m.person_id = i.person_id AND m.organization_id = i.organization_id
JOIN organizations o ON o.id = i.organization_id
WHERE t.identity_id IS NULL
  AND t.type = i.type
  AND LOWER(t.identifier) = LOWER(i.identifier)
  AND LOWER(t.domain) = LOWER(o.slug);
CREATE INDEX IF NOT EXISTS idx_oauth_tokens_membership_active
    ON oauth_tokens (membership_id, kind) WHERE revoked_at IS NULL;
