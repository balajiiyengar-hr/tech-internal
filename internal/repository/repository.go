package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	appid "tech-internal/internal/id"
	"tech-internal/internal/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrLastAdmin = errors.New("cannot remove the last organization admin")
var ErrUnknownRole = errors.New("unknown role")
var ErrInvalidRoleAssignment = errors.New("at least one role is required")

type Pool interface {
	Begin(context.Context) (pgx.Tx, error)
	Close()
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	SendBatch(context.Context, *pgx.Batch) pgx.BatchResults
}

type UserRepository struct {
	pool Pool
}

func NewUserRepository(pool Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) Get(ctx context.Context, domain, userType, identifier string) (*models.User, error) {
	return r.get(ctx, "LOWER(o.slug) = LOWER($1)", domain, userType, identifier)
}

func (r *UserRepository) GetByOrganizationID(ctx context.Context, organizationID, userType, identifier string) (*models.User, error) {
	return r.get(ctx, "o.id = $1::uuid", organizationID, userType, identifier)
}

func (r *UserRepository) get(ctx context.Context, organizationPredicate, organization, userType, identifier string) (*models.User, error) {
	row := r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT p.id::text, m.id::text, i.id::text, o.id::text, o.slug,
		       i.type, i.identifier, i.password_hash,
		       CASE WHEN 'org_admin'=ANY(role_data.keys) THEN 'admin' ELSE role_data.keys[1] END,
		       role_data.keys, permission_data.keys,
		       m.active, COALESCE(p.display_name, ''), m.created_at
		FROM login_identities i
		JOIN organizations o ON o.id = i.organization_id
		JOIN persons p ON p.id = i.person_id
		JOIN organization_memberships m ON m.person_id = p.id AND m.organization_id = o.id
		CROSS JOIN LATERAL (
			SELECT COALESCE(ARRAY_AGG(r.key ORDER BY r.key), ARRAY['member']::text[]) AS keys
			FROM membership_roles mr JOIN organization_roles r ON r.id=mr.role_id
			WHERE mr.membership_id=m.id
		) role_data
		CROSS JOIN LATERAL (
			SELECT COALESCE(ARRAY_AGG(DISTINCT rp.permission_key ORDER BY rp.permission_key), ARRAY[]::text[]) AS keys
			FROM membership_roles mr JOIN organization_role_permissions rp ON rp.role_id=mr.role_id
			WHERE mr.membership_id=m.id
		) permission_data
		WHERE %s AND i.type = $2 AND LOWER(i.identifier) = LOWER($3)
	`, organizationPredicate), organization, userType, identifier)

	var u models.User
	var pw *string
	if err := row.Scan(&u.PersonID, &u.MembershipID, &u.IdentityID, &u.OrganizationID,
		&u.Domain, &u.Type, &u.Identifier, &pw, &u.Role, &u.Roles, &u.Permissions,
		&u.Active, &u.DisplayName, &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.PasswordHash = pw
	return &u, nil
}

func (r *UserRepository) ListByDomain(
	ctx context.Context,
	domain string,
	limit, offset int,
) ([]models.User, int64, error) {
	var total int64
	if err := r.pool.QueryRow(
		ctx, `SELECT COUNT(*) FROM login_identities i JOIN organizations o ON o.id=i.organization_id WHERE LOWER(o.slug)=LOWER($1)`, domain,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `
		WITH selected_memberships AS MATERIALIZED (
			SELECT m.*
			FROM organization_memberships m
			JOIN organizations o ON o.id=m.organization_id
			WHERE LOWER(o.slug)=LOWER($1)
			ORDER BY m.created_at DESC
			LIMIT $2 OFFSET $3
		)
		SELECT p.id::text, m.id::text, i.id::text, o.id::text, o.slug,
		       i.type, i.identifier,
		       CASE WHEN 'org_admin'=ANY(role_data.keys) THEN 'admin' ELSE role_data.keys[1] END,
		       role_data.keys, permission_data.keys,
		       m.active, COALESCE(p.display_name,''), m.created_at
		FROM selected_memberships m
		JOIN organizations o ON o.id=m.organization_id
		JOIN persons p ON p.id=m.person_id
		JOIN login_identities i ON i.person_id=p.id AND i.organization_id=m.organization_id
		CROSS JOIN LATERAL (
			SELECT COALESCE(ARRAY_AGG(r.key ORDER BY r.key),ARRAY['member']::text[]) AS keys
			FROM membership_roles mr JOIN organization_roles r ON r.id=mr.role_id
			WHERE mr.membership_id=m.id
		) role_data
		CROSS JOIN LATERAL (
			SELECT COALESCE(ARRAY_AGG(DISTINCT rp.permission_key ORDER BY rp.permission_key),ARRAY[]::text[]) AS keys
			FROM membership_roles mr JOIN organization_role_permissions rp ON rp.role_id=mr.role_id
			WHERE mr.membership_id=m.id
		) permission_data
		ORDER BY m.created_at DESC
	`, domain, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.PersonID, &u.MembershipID, &u.IdentityID, &u.OrganizationID,
			&u.Domain, &u.Type, &u.Identifier, &u.Role, &u.Roles, &u.Permissions,
			&u.Active, &u.DisplayName, &u.CreatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

func (r *UserRepository) Create(ctx context.Context, u models.User, passwordHash *string) error {
	personID, err := appid.NewUUIDv7()
	if err != nil {
		return err
	}
	membershipID, err := appid.NewUUIDv7()
	if err != nil {
		return err
	}
	identityID, err := appid.NewUUIDv7()
	if err != nil {
		return err
	}
	roleKey := u.Role
	if roleKey == models.RoleAdmin {
		roleKey = models.RoleOrgAdmin
	}
	if roleKey == models.RoleUser || roleKey == "" {
		roleKey = "member"
	}
	tag, err := r.pool.Exec(ctx, `
		WITH org AS (
			SELECT o.id,r.id AS role_id
			FROM organizations o
			JOIN organization_roles r ON r.organization_id=o.id AND r.key=$10
			WHERE LOWER(o.slug)=LOWER($1)
		), new_person AS (
			INSERT INTO persons(id,display_name) SELECT $2::uuid,$3 FROM org RETURNING id
		), new_membership AS (
			INSERT INTO organization_memberships(id,organization_id,person_id,active)
			SELECT $4::uuid,org.id,new_person.id,$5 FROM org,new_person RETURNING id,organization_id
		), new_identity AS (
			INSERT INTO login_identities(id,organization_id,person_id,type,identifier,password_hash)
			SELECT $6::uuid,org.id,new_person.id,$7,$8,$9 FROM org,new_person RETURNING id
		)
		INSERT INTO membership_roles(membership_id,role_id)
		SELECT new_membership.id,org.role_id FROM new_membership,org
	`, u.Domain, personID, u.DisplayName, membershipID, u.Active, identityID, u.Type, u.Identifier, passwordHash, roleKey)
	if err != nil {
		if strings.Contains(err.Error(), "uq_login_identities") {
			return ErrConflict
		}
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("unknown role %q", roleKey)
	}
	return nil
}

func (r *UserRepository) UpdatePasswordHash(ctx context.Context, domain, userType, identifier, passwordHash string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE login_identities i
		SET password_hash = $4, updated_at = NOW()
		FROM organizations o
		WHERE i.organization_id=o.id AND LOWER(o.slug)=LOWER($1)
		  AND i.type=$2 AND LOWER(i.identifier)=LOWER($3)
	`, domain, userType, identifier, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type DomainRepository struct {
	pool Pool
}

func NewDomainRepository(pool Pool) *DomainRepository {
	return &DomainRepository{pool: pool}
}

func (r *DomainRepository) Get(ctx context.Context, domain string) (*models.DomainSettings, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT domain, email_login_enabled, sms_login_enabled
		FROM domains
		WHERE domain = $1
	`, domain)

	var d models.DomainSettings
	if err := row.Scan(&d.Domain, &d.EmailLoginEnabled, &d.SMSLoginEnabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

func (r *DomainRepository) Update(ctx context.Context, d models.DomainSettings) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE domains
		SET email_login_enabled = $2,
		    sms_login_enabled = $3,
		    updated_at = NOW()
		WHERE domain = $1
	`, d.Domain, d.EmailLoginEnabled, d.SMSLoginEnabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type AppRepository struct {
	pool Pool
}

func NewAppRepository(pool Pool) *AppRepository {
	return &AppRepository{pool: pool}
}

func (r *AppRepository) ListByDomain(
	ctx context.Context,
	domain string,
	limit, offset int,
) ([]models.PortalApp, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM portal_apps WHERE domain = $1 AND active = TRUE
	`, domain).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, domain, section, name, url, COALESCE(icon_url, ''), sort_order, active
		FROM portal_apps
		WHERE domain = $1 AND active = TRUE
		ORDER BY section, sort_order, name
		LIMIT $2 OFFSET $3
	`, domain, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var apps []models.PortalApp
	for rows.Next() {
		var a models.PortalApp
		if err := rows.Scan(&a.ID, &a.Domain, &a.Section, &a.Name, &a.URL, &a.IconURL, &a.SortOrder, &a.Active); err != nil {
			return nil, 0, err
		}
		apps = append(apps, a)
	}
	return apps, total, rows.Err()
}

func (r *AppRepository) Create(ctx context.Context, app *models.PortalApp) error {
	if app.ID == "" {
		var err error
		app.ID, err = appid.NewUUIDv7()
		if err != nil {
			return err
		}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO portal_apps (id, domain, section, name, url, icon_url, sort_order, active)
		VALUES ($1::uuid, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)
	`, app.ID, app.Domain, app.Section, app.Name, app.URL, app.IconURL, app.SortOrder, app.Active)
	return err
}

func (r *AppRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM portal_apps WHERE id = $1::uuid`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
