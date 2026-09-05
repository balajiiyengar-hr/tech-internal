-- 004 attached oauth_tokens to the V2 identity tables with the default NO ACTION
-- delete rule. Because deleting a user deletes the persons row and relies on
-- ON DELETE CASCADE to clear organization_memberships and login_identities,
-- every user that had ever been issued a token became undeletable: the cascade
-- tripped these constraints and the whole statement failed with SQLSTATE 23503.
-- Tokens are session state and must not outlive the membership they belong to.

DO $$
DECLARE
    fk RECORD;
BEGIN
    FOR fk IN
        SELECT * FROM (VALUES
            ('oauth_tokens_membership_id_fkey', 'membership_id', 'organization_memberships'),
            ('oauth_tokens_identity_id_fkey', 'identity_id', 'login_identities'),
            ('oauth_tokens_organization_id_fkey', 'organization_id', 'organizations')
        ) AS t(conname, column_name, target_table)
    LOOP
        -- confdeltype 'c' is ON DELETE CASCADE; skip constraints already migrated
        -- so that re-running this file on startup stays cheap and idempotent.
        CONTINUE WHEN NOT EXISTS (
            SELECT 1 FROM pg_constraint
            WHERE conname = fk.conname
              AND conrelid = 'oauth_tokens'::regclass
              AND confdeltype <> 'c'
        );

        EXECUTE format('ALTER TABLE oauth_tokens DROP CONSTRAINT %I', fk.conname);
        EXECUTE format(
            'ALTER TABLE oauth_tokens ADD CONSTRAINT %I FOREIGN KEY (%I) REFERENCES %I(id) ON DELETE CASCADE',
            fk.conname, fk.column_name, fk.target_table
        );
    END LOOP;
END $$;
