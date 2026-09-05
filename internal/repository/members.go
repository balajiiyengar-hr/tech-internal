package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	appid "tech-internal/internal/id"
	"tech-internal/internal/models"

	"github.com/jackc/pgx/v5"
)

type MemberRepository struct {
	pool      Pool
	authUsers *CachedUserRepository
}

func NewMemberRepository(pool Pool, authUsers ...*CachedUserRepository) *MemberRepository {
	r := &MemberRepository{pool: pool}
	if len(authUsers) > 0 {
		r.authUsers = authUsers[0]
	}
	return r
}

func (r *MemberRepository) List(ctx context.Context, organizationID string, limit, offset int) ([]models.Member, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM organization_memberships WHERE organization_id=$1::uuid`, organizationID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.id::text,p.id::text,o.id::text,o.slug,p.display_name,m.active,m.created_at
		FROM organization_memberships m
		JOIN persons p ON p.id=m.person_id
		JOIN organizations o ON o.id=m.organization_id
		WHERE m.organization_id=$1::uuid
		ORDER BY m.created_at DESC LIMIT $2 OFFSET $3`, organizationID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var out []models.Member
	for rows.Next() {
		var m models.Member
		if err := rows.Scan(&m.ID, &m.PersonID, &m.OrganizationID, &m.Organization, &m.DisplayName, &m.Active, &m.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()
	var batch pgx.Batch
	for i := range out {
		batch.Queue(`SELECT type,identifier FROM login_identities WHERE person_id=$1::uuid ORDER BY type,identifier`, out[i].PersonID)
		batch.Queue(`SELECT r.key FROM membership_roles mr JOIN organization_roles r ON r.id=mr.role_id WHERE mr.membership_id=$1::uuid ORDER BY r.key`, out[i].ID)
		batch.Queue(`SELECT DISTINCT rp.permission_key FROM membership_roles mr JOIN organization_role_permissions rp ON rp.role_id=mr.role_id WHERE mr.membership_id=$1::uuid ORDER BY rp.permission_key`, out[i].ID)
	}
	results := r.pool.SendBatch(ctx, &batch)
	defer results.Close()
	for i := range out {
		detailRows, err := results.Query()
		if err != nil {
			return nil, 0, err
		}
		for detailRows.Next() {
			var identity models.IdentityInput
			if err := detailRows.Scan(&identity.Type, &identity.Identifier); err != nil {
				detailRows.Close()
				return nil, 0, err
			}
			out[i].Identities = append(out[i].Identities, identity)
		}
		detailRows.Close()
		roleRows, err := results.Query()
		if err != nil {
			return nil, 0, err
		}
		for roleRows.Next() {
			var key string
			if err := roleRows.Scan(&key); err != nil {
				roleRows.Close()
				return nil, 0, err
			}
			out[i].RoleKeys = append(out[i].RoleKeys, key)
		}
		roleRows.Close()
		permissionRows, err := results.Query()
		if err != nil {
			return nil, 0, err
		}
		for permissionRows.Next() {
			var key string
			if err := permissionRows.Scan(&key); err != nil {
				permissionRows.Close()
				return nil, 0, err
			}
			out[i].Permissions = append(out[i].Permissions, key)
		}
		permissionRows.Close()
	}
	return out, total, results.Close()
}

func (r *MemberRepository) Get(ctx context.Context, organizationID, membershipID string) (*models.Member, error) {
	var m models.Member
	err := r.pool.QueryRow(ctx, `
		SELECT m.id::text,p.id::text,o.id::text,o.slug,p.display_name,m.active,m.created_at
		FROM organization_memberships m JOIN persons p ON p.id=m.person_id
		JOIN organizations o ON o.id=m.organization_id
		WHERE m.id=$1::uuid AND m.organization_id=$2::uuid`, membershipID, organizationID).
		Scan(&m.ID, &m.PersonID, &m.OrganizationID, &m.Organization, &m.DisplayName, &m.Active, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := r.loadDetails(ctx, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *MemberRepository) loadDetails(ctx context.Context, m *models.Member) error {
	rows, err := r.pool.Query(ctx, `SELECT type,identifier FROM login_identities WHERE person_id=$1::uuid ORDER BY type,identifier`, m.PersonID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var i models.IdentityInput
		if err := rows.Scan(&i.Type, &i.Identifier); err != nil {
			rows.Close()
			return err
		}
		m.Identities = append(m.Identities, i)
	}
	rows.Close()
	rows, err = r.pool.Query(ctx, `SELECT r.key FROM membership_roles mr JOIN organization_roles r ON r.id=mr.role_id WHERE mr.membership_id=$1::uuid ORDER BY r.key`, m.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		m.RoleKeys = append(m.RoleKeys, key)
	}
	rows.Close()
	rows, err = r.pool.Query(ctx, `SELECT DISTINCT rp.permission_key FROM membership_roles mr JOIN organization_role_permissions rp ON rp.role_id=mr.role_id WHERE mr.membership_id=$1::uuid ORDER BY rp.permission_key`, m.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		m.Permissions = append(m.Permissions, key)
	}
	rows.Close()
	return rows.Err()
}

// Create stores one person, exactly one membership, and one or more globally
// unique identities. IdentityInput.Password contains an already protected hash.
func (r *MemberRepository) Create(ctx context.Context, organizationID string, m *models.Member) error {
	if len(m.Identities) == 0 {
		return fmt.Errorf("at least one identity is required")
	}
	var err error
	m.PersonID, err = appid.NewUUIDv7()
	if err != nil {
		return err
	}
	m.ID, err = appid.NewUUIDv7()
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO persons(id,display_name) VALUES($1::uuid,$2)`, m.PersonID, m.DisplayName); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(id,organization_id,person_id,active) VALUES($1::uuid,$2::uuid,$3::uuid,TRUE)`, m.ID, organizationID, m.PersonID); err != nil {
		return err
	}
	var identityBatch pgx.Batch
	for _, identity := range m.Identities {
		identityID, e := appid.NewUUIDv7()
		if e != nil {
			return e
		}
		var hash *string
		if identity.Password != "" {
			hash = &identity.Password
		}
		identityBatch.Queue(`INSERT INTO login_identities(id,organization_id,person_id,type,identifier,password_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6)`, identityID, organizationID, m.PersonID, identity.Type, identity.Identifier, hash)
	}
	identityResults := tx.SendBatch(ctx, &identityBatch)
	for range m.Identities {
		if _, e := identityResults.Exec(); e != nil {
			_ = identityResults.Close()
			if strings.Contains(e.Error(), "uq_login_identities") {
				return ErrConflict
			}
			return e
		}
	}
	if err := identityResults.Close(); err != nil {
		return err
	}
	if len(m.RoleKeys) == 0 {
		m.RoleKeys = []string{"member"}
	}
	var roleBatch pgx.Batch
	for _, key := range m.RoleKeys {
		roleBatch.Queue(`INSERT INTO membership_roles(membership_id,role_id) SELECT $1::uuid,id FROM organization_roles WHERE organization_id=$2::uuid AND LOWER(key)=LOWER($3)`, m.ID, organizationID, key)
	}
	roleResults := tx.SendBatch(ctx, &roleBatch)
	for _, key := range m.RoleKeys {
		tag, e := roleResults.Exec()
		if e != nil {
			_ = roleResults.Close()
			return e
		}
		if tag.RowsAffected() != 1 {
			_ = roleResults.Close()
			return fmt.Errorf("unknown role %q", key)
		}
	}
	if err := roleResults.Close(); err != nil {
		return err
	}
	m.OrganizationID = organizationID
	m.Active = true
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if r.authUsers != nil {
		for _, identity := range m.Identities {
			if err := r.authUsers.loadByOrganizationAndPut(ctx, organizationID, identity.Type, identity.Identifier); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *MemberRepository) Update(ctx context.Context, organizationID, membershipID, displayName string, active *bool, roleKeys []string) error {
	var cachedPersonID string
	if r.authUsers != nil {
		member, err := r.Get(ctx, organizationID, membershipID)
		if err != nil {
			return err
		}
		cachedPersonID = member.PersonID
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, organizationID); err != nil {
		return err
	}
	var personID string
	var wasAdmin bool
	err = tx.QueryRow(ctx, `SELECT m.person_id::text,EXISTS(SELECT 1 FROM membership_roles mr JOIN organization_roles r ON r.id=mr.role_id WHERE mr.membership_id=m.id AND r.key='org_admin') FROM organization_memberships m WHERE m.id=$1::uuid AND m.organization_id=$2::uuid FOR UPDATE`, membershipID, organizationID).Scan(&personID, &wasAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if roleKeys != nil {
		if len(roleKeys) == 0 {
			return ErrInvalidRoleAssignment
		}
		normalized := make([]string, 0, len(roleKeys))
		seen := make(map[string]struct{}, len(roleKeys))
		for _, requestedKey := range roleKeys {
			key := strings.ToLower(strings.TrimSpace(requestedKey))
			if key == "" {
				return fmt.Errorf("%w %q", ErrUnknownRole, requestedKey)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("%w: duplicate role %q", ErrInvalidRoleAssignment, key)
			}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_roles WHERE organization_id=$1::uuid AND LOWER(key)=$2)`, organizationID, key).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%w %q", ErrUnknownRole, requestedKey)
			}
			seen[key] = struct{}{}
			normalized = append(normalized, key)
		}
		roleKeys = normalized
	}
	willAdmin := wasAdmin
	if roleKeys != nil {
		willAdmin = false
		for _, k := range roleKeys {
			if k == "org_admin" {
				willAdmin = true
			}
		}
	}
	disabling := active != nil && !*active
	if wasAdmin && (!willAdmin || disabling) {
		if err := ensureAnotherAdmin(ctx, tx, organizationID, membershipID); err != nil {
			return err
		}
	}
	if displayName != "" {
		if _, err = tx.Exec(ctx, `UPDATE persons SET display_name=$2,updated_at=NOW() WHERE id=$1::uuid`, personID, displayName); err != nil {
			return err
		}
	}
	if active != nil {
		if _, err = tx.Exec(ctx, `UPDATE organization_memberships SET active=$3,updated_at=NOW() WHERE id=$1::uuid AND organization_id=$2::uuid`, membershipID, organizationID, *active); err != nil {
			return err
		}
	}
	if roleKeys != nil {
		if _, err = tx.Exec(ctx, `DELETE FROM membership_roles WHERE membership_id=$1::uuid`, membershipID); err != nil {
			return err
		}
		for _, key := range roleKeys {
			tag, e := tx.Exec(ctx, `INSERT INTO membership_roles SELECT $1::uuid,id FROM organization_roles WHERE organization_id=$2::uuid AND LOWER(key)=$3`, membershipID, organizationID, key)
			if e != nil {
				return e
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("%w %q", ErrUnknownRole, key)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if r.authUsers != nil {
		return r.authUsers.invalidatePerson(ctx, cachedPersonID)
	}
	return nil
}

func (r *MemberRepository) Delete(ctx context.Context, organizationID, membershipID string) error {
	var cachedPersonID string
	if r.authUsers != nil {
		member, err := r.Get(ctx, organizationID, membershipID)
		if err != nil {
			return err
		}
		cachedPersonID = member.PersonID
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, organizationID); err != nil {
		return err
	}
	var personID string
	var admin bool
	err = tx.QueryRow(ctx, `SELECT m.person_id::text,EXISTS(SELECT 1 FROM membership_roles mr JOIN organization_roles r ON r.id=mr.role_id WHERE mr.membership_id=m.id AND r.key='org_admin') FROM organization_memberships m WHERE m.id=$1::uuid AND m.organization_id=$2::uuid FOR UPDATE`, membershipID, organizationID).Scan(&personID, &admin)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if admin {
		if err := ensureAnotherAdmin(ctx, tx, organizationID, membershipID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM persons WHERE id=$1::uuid`, personID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if r.authUsers != nil {
		return r.authUsers.invalidatePerson(ctx, cachedPersonID)
	}
	return nil
}

// DeleteByIdentity removes the person behind one login identity. The membership
// is resolved inside the transaction rather than passed in, so a caller holding
// a stale or V1-shaped lookup result cannot delete the wrong row or fail on an
// empty membership id. organizationID may be empty for tokens minted before the
// V2 rollout, in which case the organization is resolved from the domain slug.
func (r *MemberRepository) DeleteByIdentity(ctx context.Context, organizationID, domain, userType, identifier string) error {
	var cachedPersonID string
	if r.authUsers != nil {
		user, err := r.authUsers.Get(ctx, domain, userType, identifier)
		if err != nil {
			return err
		}
		cachedPersonID = user.PersonID
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	orgID := strings.TrimSpace(organizationID)
	if orgID == "" {
		err = tx.QueryRow(ctx, `SELECT o.id::text FROM organizations o WHERE LOWER(o.slug)=LOWER($1)`, domain).Scan(&orgID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, orgID); err != nil {
		return err
	}

	var personID, membershipID string
	var admin bool
	err = tx.QueryRow(ctx, `
		SELECT m.person_id::text,m.id::text,EXISTS(SELECT 1 FROM membership_roles mr JOIN organization_roles r ON r.id=mr.role_id WHERE mr.membership_id=m.id AND r.key='org_admin')
		FROM login_identities i
		JOIN organization_memberships m ON m.person_id=i.person_id AND m.organization_id=i.organization_id
		WHERE i.organization_id=$1::uuid AND i.type=$2 AND LOWER(i.identifier)=LOWER($3)
		FOR UPDATE`, orgID, userType, identifier).Scan(&personID, &membershipID, &admin)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if admin {
		if err := ensureAnotherAdmin(ctx, tx, orgID, membershipID); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM persons WHERE id=$1::uuid`, personID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if r.authUsers != nil {
		return r.authUsers.invalidatePerson(ctx, cachedPersonID)
	}
	return nil
}

type dbtx interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ensureAnotherAdmin(ctx context.Context, tx dbtx, organizationID, membershipID string) error {
	var n int
	err := tx.QueryRow(ctx, `SELECT COUNT(DISTINCT m.id) FROM organization_memberships m JOIN membership_roles mr ON mr.membership_id=m.id JOIN organization_roles r ON r.id=mr.role_id WHERE m.organization_id=$1::uuid AND m.id<>$2::uuid AND m.active AND r.key='org_admin'`, organizationID, membershipID).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

func (r *MemberRepository) ListRoles(ctx context.Context, organizationID string, limit, offset int) ([]models.OrganizationRole, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM organization_roles WHERE organization_id=$1::uuid`, organizationID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id::text,key,name,is_system FROM organization_roles WHERE organization_id=$1::uuid ORDER BY is_system DESC,name LIMIT $2 OFFSET $3`, organizationID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []models.OrganizationRole
	for rows.Next() {
		var role models.OrganizationRole
		if err := rows.Scan(&role.ID, &role.Key, &role.Name, &role.IsSystem); err != nil {
			return nil, 0, err
		}
		p, err := r.pool.Query(ctx, `SELECT permission_key FROM organization_role_permissions WHERE role_id=$1::uuid ORDER BY permission_key`, role.ID)
		if err != nil {
			return nil, 0, err
		}
		for p.Next() {
			var key string
			if err := p.Scan(&key); err != nil {
				p.Close()
				return nil, 0, err
			}
			role.Permissions = append(role.Permissions, key)
		}
		p.Close()
		out = append(out, role)
	}
	return out, total, rows.Err()
}

func (r *MemberRepository) CreateRole(ctx context.Context, organizationID, key, name string, permissions []string) (*models.OrganizationRole, error) {
	id, err := appid.NewUUIDv7()
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO organization_roles(id,organization_id,key,name,is_system) VALUES($1::uuid,$2::uuid,$3,$4,FALSE)`, id, organizationID, key, name); err != nil {
		if strings.Contains(err.Error(), "uq_organization_roles") {
			return nil, ErrConflict
		}
		return nil, err
	}
	for _, p := range permissions {
		tag, e := tx.Exec(ctx, `INSERT INTO organization_role_permissions(role_id,permission_key) SELECT $1::uuid,key FROM permissions WHERE key=$2`, id, p)
		if e != nil {
			return nil, e
		}
		if tag.RowsAffected() != 1 {
			return nil, fmt.Errorf("unknown permission %q", p)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &models.OrganizationRole{ID: id, Key: key, Name: name, Permissions: permissions}, nil
}
