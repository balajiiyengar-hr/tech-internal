package repository_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	appid "tech-internal/internal/id"
	"tech-internal/internal/models"
	"tech-internal/internal/repository"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the real delete path against the person/membership/
// identity cascade, so they need a database. Point TEST_DATABASE_URL at a
// scratch instance that has the migrations applied, e.g.
//
//	TEST_DATABASE_URL=postgres://portal:portal@127.0.0.1:5434/tech_internal?sslmode=disable go test ./internal/repository/
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newOrg creates an isolated organization with the system roles the repository
// expects, and removes it (and everything cascading from it) afterwards.
func newOrg(t *testing.T, pool *pgxpool.Pool) (id, slug string) {
	t.Helper()
	ctx := context.Background()
	id, err := appid.NewUUIDv7()
	if err != nil {
		t.Fatalf("new org id: %v", err)
	}
	slug = fmt.Sprintf("deltest-%d.example", time.Now().UnixNano())
	if _, err := pool.Exec(ctx,
		`INSERT INTO organizations(id,slug,name) VALUES($1::uuid,$2,$2)`, id, slug); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	for _, key := range []string{"org_admin", "member"} {
		roleID, err := appid.NewUUIDv7()
		if err != nil {
			t.Fatalf("new role id: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO organization_roles(id,organization_id,key,name,is_system) VALUES($1::uuid,$2::uuid,$3,$3,TRUE)`,
			roleID, id, key); err != nil {
			t.Fatalf("insert role %s: %v", key, err)
		}
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1::uuid`, id); err != nil {
			t.Logf("cleanup organization %s: %v", id, err)
		}
	})
	return id, slug
}

func newMember(t *testing.T, repo *repository.MemberRepository, orgID, identifier string, roleKeys ...string) *models.Member {
	t.Helper()
	if len(roleKeys) == 0 {
		roleKeys = []string{"member"}
	}
	m := &models.Member{
		DisplayName: "delete probe",
		RoleKeys:    roleKeys,
		Identities:  []models.IdentityInput{{Type: models.UserTypeSMS, Identifier: identifier}},
	}
	if err := repo.Create(context.Background(), orgID, m); err != nil {
		t.Fatalf("create member %s: %v", identifier, err)
	}
	return m
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// Regression for the 500s on DELETE /admin/users/:type/:identifier: deleting
// the person must cleanly cascade into organization_memberships and
// login_identities.
func TestDeleteByIdentityRemovesMember(t *testing.T) {
	pool := testPool(t)
	repo := repository.NewMemberRepository(pool)
	orgID, slug := newOrg(t, pool)
	identifier := fmt.Sprintf("+1555%010d", time.Now().UnixNano()%1e10)

	m := newMember(t, repo, orgID, identifier)

	if err := repo.DeleteByIdentity(context.Background(), orgID, slug, models.UserTypeSMS, identifier); err != nil {
		t.Fatalf("DeleteByIdentity() = %v, want nil", err)
	}

	if n := countRows(t, pool, `SELECT COUNT(*) FROM persons WHERE id=$1::uuid`, m.PersonID); n != 0 {
		t.Errorf("persons rows = %d, want 0", n)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM login_identities WHERE person_id=$1::uuid`, m.PersonID); n != 0 {
		t.Errorf("login_identities rows = %d, want 0", n)
	}
}

func TestDeleteByIdentityMissingUser(t *testing.T) {
	pool := testPool(t)
	repo := repository.NewMemberRepository(pool)
	orgID, slug := newOrg(t, pool)

	err := repo.DeleteByIdentity(context.Background(), orgID, slug, models.UserTypeSMS, "+15559999999")

	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("DeleteByIdentity() = %v, want ErrNotFound", err)
	}
}

func TestDeleteByIdentityIsScopedToOrganization(t *testing.T) {
	pool := testPool(t)
	repo := repository.NewMemberRepository(pool)
	ownerOrgID, ownerSlug := newOrg(t, pool)
	otherOrgID, otherSlug := newOrg(t, pool)
	identifier := fmt.Sprintf("+1555%010d", time.Now().UnixNano()%1e10)

	m := newMember(t, repo, ownerOrgID, identifier)

	err := repo.DeleteByIdentity(context.Background(), otherOrgID, otherSlug, models.UserTypeSMS, identifier)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("cross-organization delete = %v, want ErrNotFound", err)
	}
	if n := countRows(t, pool, `SELECT COUNT(*) FROM persons WHERE id=$1::uuid`, m.PersonID); n != 1 {
		t.Fatalf("persons rows = %d, want 1: the member was deleted from another organization", n)
	}
	if err := repo.DeleteByIdentity(context.Background(), ownerOrgID, ownerSlug, models.UserTypeSMS, identifier); err != nil {
		t.Fatalf("owning-organization delete = %v, want nil", err)
	}
}

// Tokens minted before the V2 rollout carry no organization id, so the slug has
// to be enough to locate the member.
func TestDeleteByIdentityResolvesOrganizationFromDomain(t *testing.T) {
	pool := testPool(t)
	repo := repository.NewMemberRepository(pool)
	orgID, slug := newOrg(t, pool)
	identifier := fmt.Sprintf("+1555%010d", time.Now().UnixNano()%1e10)

	newMember(t, repo, orgID, identifier)

	if err := repo.DeleteByIdentity(context.Background(), "", slug, models.UserTypeSMS, identifier); err != nil {
		t.Fatalf("DeleteByIdentity() with empty organization id = %v, want nil", err)
	}
}

func TestDeleteByIdentityProtectsLastAdmin(t *testing.T) {
	pool := testPool(t)
	repo := repository.NewMemberRepository(pool)
	orgID, slug := newOrg(t, pool)
	adminID := fmt.Sprintf("+1555%010d", time.Now().UnixNano()%1e10)

	newMember(t, repo, orgID, adminID, "org_admin")

	err := repo.DeleteByIdentity(context.Background(), orgID, slug, models.UserTypeSMS, adminID)
	if !errors.Is(err, repository.ErrLastAdmin) {
		t.Fatalf("deleting sole admin = %v, want ErrLastAdmin", err)
	}

	secondID := fmt.Sprintf("+1556%010d", time.Now().UnixNano()%1e10)
	newMember(t, repo, orgID, secondID, "org_admin")

	if err := repo.DeleteByIdentity(context.Background(), orgID, slug, models.UserTypeSMS, adminID); err != nil {
		t.Fatalf("deleting one of two admins = %v, want nil", err)
	}
}

// The mixed load test deletes the same identifier from several VUs at once.
// Exactly one caller may win; the rest must see ErrNotFound, never a driver error.
func TestDeleteByIdentityConcurrentDeleteIsIdempotent(t *testing.T) {
	pool := testPool(t)
	repo := repository.NewMemberRepository(pool)
	orgID, slug := newOrg(t, pool)
	identifier := fmt.Sprintf("+1555%010d", time.Now().UnixNano()%1e10)

	newMember(t, repo, orgID, identifier)

	const goroutines = 16
	results := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = repo.DeleteByIdentity(context.Background(), orgID, slug, models.UserTypeSMS, identifier)
		}(i)
	}
	wg.Wait()

	var deleted, notFound int
	for _, err := range results {
		switch {
		case err == nil:
			deleted++
		case errors.Is(err, repository.ErrNotFound):
			notFound++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if deleted != 1 {
		t.Errorf("got %d successful deletes, want exactly 1", deleted)
	}
	if notFound != goroutines-1 {
		t.Errorf("got %d ErrNotFound, want %d", notFound, goroutines-1)
	}
}
