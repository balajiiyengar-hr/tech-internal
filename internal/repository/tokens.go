package repository

import (
	"context"
	"errors"

	appid "tech-internal/internal/id"
	"tech-internal/internal/models"

	"github.com/jackc/pgx/v5"
)

type TokenRepository struct {
	pool Pool
}

func NewTokenRepository(pool Pool) *TokenRepository {
	return &TokenRepository{pool: pool}
}

func (r *TokenRepository) Insert(ctx context.Context, rec models.OAuthToken, tokenHash string) error {
	if rec.ID == "" {
		var err error
		rec.ID, err = appid.NewUUIDv7()
		if err != nil {
			return err
		}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO oauth_tokens (id, jti, family_id, domain, type, identifier, kind, token_hash, expires_at, membership_id, identity_id, organization_id)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10,'')::uuid, NULLIF($11,'')::uuid, NULLIF($12,'')::uuid)
	`, rec.ID, rec.JTI, rec.FamilyID, rec.Domain, rec.Type, rec.Identifier, rec.Kind, tokenHash, rec.ExpiresAt, rec.MembershipID, rec.IdentityID, rec.OrganizationID)
	return err
}

// InsertPair persists both halves of a token pair atomically.
func (r *TokenRepository) InsertPair(
	ctx context.Context,
	access models.OAuthToken,
	accessHash string,
	refresh models.OAuthToken,
	refreshHash string,
) error {
	var err error
	if access.ID == "" {
		access.ID, err = appid.NewUUIDv7()
		if err != nil {
			return err
		}
	}
	if refresh.ID == "" {
		refresh.ID, err = appid.NewUUIDv7()
		if err != nil {
			return err
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const query = `
		INSERT INTO oauth_tokens
			(id, jti, family_id, domain, type, identifier, kind, token_hash, expires_at, membership_id, identity_id, organization_id)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10,'')::uuid, NULLIF($11,'')::uuid, NULLIF($12,'')::uuid)
	`
	var batch pgx.Batch
	batch.Queue(query,
		access.ID, access.JTI, access.FamilyID, access.Domain, access.Type,
		access.Identifier, access.Kind, accessHash, access.ExpiresAt, access.MembershipID, access.IdentityID, access.OrganizationID,
	)
	batch.Queue(query,
		refresh.ID, refresh.JTI, refresh.FamilyID, refresh.Domain, refresh.Type,
		refresh.Identifier, refresh.Kind, refreshHash, refresh.ExpiresAt, refresh.MembershipID, refresh.IdentityID, refresh.OrganizationID,
	)
	results := tx.SendBatch(ctx, &batch)
	if _, err := results.Exec(); err != nil {
		_ = results.Close()
		return err
	}
	if _, err := results.Exec(); err != nil {
		_ = results.Close()
		return err
	}
	if err := results.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RotatePair consumes oldRefreshJTI once and atomically stores a new pair.
func (r *TokenRepository) RotatePair(
	ctx context.Context,
	oldRefreshJTI string,
	access models.OAuthToken,
	accessHash string,
	refresh models.OAuthToken,
	refreshHash string,
) (bool, error) {
	var err error
	if access.ID == "" {
		access.ID, err = appid.NewUUIDv7()
		if err != nil {
			return false, err
		}
	}
	if refresh.ID == "" {
		refresh.ID, err = appid.NewUUIDv7()
		if err != nil {
			return false, err
		}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE oauth_tokens
		SET revoked_at = NOW()
		WHERE jti = $1
		  AND kind = 'refresh'
		  AND revoked_at IS NULL
		  AND expires_at > NOW()
	`, oldRefreshJTI)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}

	const query = `
		INSERT INTO oauth_tokens
			(id, jti, family_id, domain, type, identifier, kind, token_hash, expires_at, membership_id, identity_id, organization_id)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10,'')::uuid, NULLIF($11,'')::uuid, NULLIF($12,'')::uuid)
	`
	var batch pgx.Batch
	batch.Queue(query,
		access.ID, access.JTI, access.FamilyID, access.Domain, access.Type,
		access.Identifier, access.Kind, accessHash, access.ExpiresAt, access.MembershipID, access.IdentityID, access.OrganizationID,
	)
	batch.Queue(query,
		refresh.ID, refresh.JTI, refresh.FamilyID, refresh.Domain, refresh.Type,
		refresh.Identifier, refresh.Kind, refreshHash, refresh.ExpiresAt, refresh.MembershipID, refresh.IdentityID, refresh.OrganizationID,
	)
	results := tx.SendBatch(ctx, &batch)
	if _, err := results.Exec(); err != nil {
		_ = results.Close()
		return false, err
	}
	if _, err := results.Exec(); err != nil {
		_ = results.Close()
		return false, err
	}
	if err := results.Close(); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (r *TokenRepository) GetActiveByJTI(ctx context.Context, jti string) (*models.OAuthToken, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id::text, jti, family_id, domain, type, identifier, kind, expires_at, revoked_at, created_at,
		       COALESCE(membership_id::text,''),COALESCE(identity_id::text,''),COALESCE(organization_id::text,'')
		FROM oauth_tokens
		WHERE jti = $1 AND revoked_at IS NULL AND expires_at > NOW()
	`, jti)

	var t models.OAuthToken
	if err := row.Scan(&t.ID, &t.JTI, &t.FamilyID, &t.Domain, &t.Type, &t.Identifier, &t.Kind, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt, &t.MembershipID, &t.IdentityID, &t.OrganizationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (r *TokenRepository) RevokeByJTI(ctx context.Context, jti string) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE oauth_tokens SET revoked_at = NOW()
		WHERE jti = $1 AND revoked_at IS NULL
	`, jti)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *TokenRepository) RevokeFamily(ctx context.Context, familyID string) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE oauth_tokens SET revoked_at = NOW()
		WHERE family_id = $1 AND revoked_at IS NULL
	`, familyID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *TokenRepository) RevokeUser(ctx context.Context, domain, userType, identifier string) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE oauth_tokens SET revoked_at = NOW()
		WHERE domain = $1 AND type = $2 AND identifier = $3 AND revoked_at IS NULL
	`, domain, userType, identifier)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *TokenRepository) ListActiveByUser(ctx context.Context, domain, userType, identifier string) ([]models.OAuthToken, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, jti, family_id, domain, type, identifier, kind, expires_at, revoked_at, created_at,
		       COALESCE(membership_id::text,''),COALESCE(identity_id::text,''),COALESCE(organization_id::text,'')
		FROM oauth_tokens
		WHERE domain = $1 AND type = $2 AND identifier = $3 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC
	`, domain, userType, identifier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.OAuthToken
	for rows.Next() {
		var t models.OAuthToken
		if err := rows.Scan(&t.ID, &t.JTI, &t.FamilyID, &t.Domain, &t.Type, &t.Identifier, &t.Kind, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt, &t.MembershipID, &t.IdentityID, &t.OrganizationID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []models.OAuthToken{}
	}
	return out, rows.Err()
}
