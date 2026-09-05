package repository_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	appid "tech-internal/internal/id"
	"tech-internal/internal/repository"

	"github.com/jackc/pgx/v5/pgxpool"
)

func addOrganizationRole(t *testing.T, pool *pgxpool.Pool, organizationID, key string) {
	t.Helper()
	roleID, err := appid.NewUUIDv7()
	if err != nil {
		t.Fatalf("new role id: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO organization_roles(id,organization_id,key,name,is_system) VALUES($1::uuid,$2::uuid,$3,$3,TRUE)`,
		roleID, organizationID, key); err != nil {
		t.Fatalf("insert role %s: %v", key, err)
	}
}

func updateTestIdentity() string {
	return fmt.Sprintf("+1777%08d", time.Now().UnixNano()%1e8)
}

func TestUpdateMemberRoles(t *testing.T) {
	pool := testPool(t)
	repo := repository.NewMemberRepository(pool)
	ctx := context.Background()

	t.Run("assigns available role", func(t *testing.T) {
		orgID, _ := newOrg(t, pool)
		addOrganizationRole(t, pool, orgID, "finance")
		member := newMember(t, repo, orgID, updateTestIdentity())

		if err := repo.Update(ctx, orgID, member.ID, "", nil, []string{"finance"}); err != nil {
			t.Fatalf("Update() = %v, want nil", err)
		}
		var role string
		if err := pool.QueryRow(ctx, `
			SELECT r.key
			FROM membership_roles mr
			JOIN organization_roles r ON r.id=mr.role_id
			WHERE mr.membership_id=$1::uuid`, member.ID).Scan(&role); err != nil {
			t.Fatalf("load updated role: %v", err)
		}
		if role != "finance" {
			t.Fatalf("updated role = %q, want finance", role)
		}
	})

	t.Run("rejects unknown role without changing assignments", func(t *testing.T) {
		orgID, _ := newOrg(t, pool)
		member := newMember(t, repo, orgID, updateTestIdentity())

		err := repo.Update(ctx, orgID, member.ID, "", nil, []string{"unavailable"})
		if !errors.Is(err, repository.ErrUnknownRole) {
			t.Fatalf("Update() = %v, want ErrUnknownRole", err)
		}
		var role string
		if err := pool.QueryRow(ctx, `
			SELECT r.key
			FROM membership_roles mr
			JOIN organization_roles r ON r.id=mr.role_id
			WHERE mr.membership_id=$1::uuid`, member.ID).Scan(&role); err != nil {
			t.Fatalf("load original role: %v", err)
		}
		if role != "member" {
			t.Fatalf("role after rejected update = %q, want member", role)
		}
	})

	t.Run("does not update cross-organization membership", func(t *testing.T) {
		ownerOrgID, _ := newOrg(t, pool)
		otherOrgID, _ := newOrg(t, pool)
		member := newMember(t, repo, ownerOrgID, updateTestIdentity())

		err := repo.Update(ctx, otherOrgID, member.ID, "", nil, []string{"member"})
		if !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("cross-organization Update() = %v, want ErrNotFound", err)
		}
	})

	t.Run("protects last admin from demotion", func(t *testing.T) {
		orgID, _ := newOrg(t, pool)
		admin := newMember(t, repo, orgID, updateTestIdentity(), "org_admin")

		err := repo.Update(ctx, orgID, admin.ID, "", nil, []string{"member"})
		if !errors.Is(err, repository.ErrLastAdmin) {
			t.Fatalf("last-admin Update() = %v, want ErrLastAdmin", err)
		}
	})
}
