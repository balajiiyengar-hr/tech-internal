package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"tech-internal/internal/models"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

func memberColumns() []string {
	return []string{"id", "person", "org", "slug", "name", "active", "created"}
}

func TestMemberRepositoryRead(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	t.Run("list", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewMemberRepository(mock)
		mock.ExpectQuery("SELECT COUNT").WithArgs("org").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(1))
		mock.ExpectQuery("SELECT m.id").WithArgs("org", 10, 0).
			WillReturnRows(pgxmock.NewRows(memberColumns()).AddRow("m", "p", "org", "acme", "A", true, now))
		batch := mock.ExpectBatch()
		batch.ExpectQuery("SELECT type").WithArgs("p").WillReturnRows(pgxmock.NewRows([]string{"type", "identifier"}).AddRow("email", "a@b"))
		batch.ExpectQuery("SELECT r.key").WithArgs("m").WillReturnRows(pgxmock.NewRows([]string{"key"}).AddRow("member"))
		batch.ExpectQuery("SELECT DISTINCT").WithArgs("m").WillReturnRows(pgxmock.NewRows([]string{"key"}).AddRow("members:read"))
		members, total, err := repo.List(ctx, "org", 10, 0)
		if err != nil || total != 1 || len(members) != 1 || members[0].Identities[0].Identifier != "a@b" {
			t.Fatalf("List=%#v,%d,%v", members, total, err)
		}
	})

	t.Run("get", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewMemberRepository(mock)
		mock.ExpectQuery("SELECT m.id").WithArgs("m", "org").
			WillReturnRows(pgxmock.NewRows(memberColumns()).AddRow("m", "p", "org", "acme", "A", true, now))
		mock.ExpectQuery("SELECT type").WithArgs("p").WillReturnRows(pgxmock.NewRows([]string{"type", "identifier"}).AddRow("sms", "123"))
		mock.ExpectQuery("SELECT r.key").WithArgs("m").WillReturnRows(pgxmock.NewRows([]string{"key"}).AddRow("member"))
		mock.ExpectQuery("SELECT DISTINCT").WithArgs("m").WillReturnRows(pgxmock.NewRows([]string{"key"}).AddRow("members:read"))
		member, err := repo.Get(ctx, "org", "m")
		if err != nil || len(member.RoleKeys) != 1 || len(member.Permissions) != 1 {
			t.Fatalf("Get=%#v,%v", member, err)
		}
		mock.ExpectQuery("SELECT m.id").WithArgs(anyArgs(2)...).WillReturnError(pgx.ErrNoRows)
		if _, err := repo.Get(ctx, "org", "none"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing=%v", err)
		}
	})
}

func TestMemberRepositoryCreateUpdateDelete(t *testing.T) {
	ctx := context.Background()
	t.Run("create", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewMemberRepository(mock)
		if err := repo.Create(ctx, "org", &models.Member{}); err == nil {
			t.Fatal("empty identities accepted")
		}
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO persons").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("INSERT INTO organization_memberships").WithArgs(anyArgs(3)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		identities := mock.ExpectBatch()
		identities.ExpectExec("INSERT INTO login_identities").WithArgs(anyArgs(6)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		roles := mock.ExpectBatch()
		roles.ExpectExec("INSERT INTO membership_roles").WithArgs(anyArgs(3)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit()
		member := &models.Member{DisplayName: "A", Identities: []models.IdentityInput{{Type: "email", Identifier: "a@b", Password: "hash"}}}
		if err := repo.Create(ctx, "org", member); err != nil || member.ID == "" || !member.Active || member.RoleKeys[0] != "member" {
			t.Fatalf("Create=%#v,%v", member, err)
		}
	})

	t.Run("update", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewMemberRepository(mock)
		active := false
		mock.ExpectBegin()
		mock.ExpectExec("SELECT pg_advisory").WithArgs("org").WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectQuery("SELECT m.person_id").WithArgs("m", "org").WillReturnRows(pgxmock.NewRows([]string{"person", "admin"}).AddRow("p", false))
		mock.ExpectQuery("SELECT EXISTS").WithArgs("org", "member").WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectExec("UPDATE persons").WithArgs("p", "New").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectExec("UPDATE organization_memberships").WithArgs("m", "org", false).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectExec("DELETE FROM membership_roles").WithArgs("m").WillReturnResult(pgxmock.NewResult("DELETE", 1))
		mock.ExpectExec("INSERT INTO membership_roles").WithArgs("m", "org", "member").WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit()
		if err := repo.Update(ctx, "org", "m", "New", &active, []string{"member"}); err != nil {
			t.Fatal(err)
		}
		mock.ExpectBegin()
		mock.ExpectExec("SELECT pg_advisory").WithArgs("org").WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectQuery("SELECT m.person_id").WithArgs("none", "org").WillReturnError(pgx.ErrNoRows)
		mock.ExpectRollback()
		if err := repo.Update(ctx, "org", "none", "", nil, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing=%v", err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewMemberRepository(mock)
		mock.ExpectBegin()
		mock.ExpectExec("SELECT pg_advisory").WithArgs("org").WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectQuery("SELECT m.person_id").WithArgs("m", "org").WillReturnRows(pgxmock.NewRows([]string{"person", "admin"}).AddRow("p", false))
		mock.ExpectExec("DELETE FROM persons").WithArgs("p").WillReturnResult(pgxmock.NewResult("DELETE", 1))
		mock.ExpectCommit()
		if err := repo.Delete(ctx, "org", "m"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("delete identity", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewMemberRepository(mock)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT o.id").WithArgs("acme").WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("org"))
		mock.ExpectExec("SELECT pg_advisory").WithArgs("org").WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectQuery("SELECT m.person_id").WithArgs("org", "email", "a@b").WillReturnRows(pgxmock.NewRows([]string{"person", "member", "admin"}).AddRow("p", "m", false))
		mock.ExpectExec("DELETE FROM persons").WithArgs("p").WillReturnResult(pgxmock.NewResult("DELETE", 1))
		mock.ExpectCommit()
		if err := repo.DeleteByIdentity(ctx, "", "acme", "email", "a@b"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMemberRepositoryRoles(t *testing.T) {
	ctx := context.Background()
	mock := mockPool(t)
	repo := NewMemberRepository(mock)
	mock.ExpectQuery("SELECT COUNT").WithArgs("org").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(1))
	mock.ExpectQuery("SELECT id").WithArgs("org", 10, 0).WillReturnRows(pgxmock.NewRows([]string{"id", "key", "name", "system"}).AddRow("r", "member", "Member", true))
	mock.ExpectQuery("SELECT permission_key").WithArgs("r").WillReturnRows(pgxmock.NewRows([]string{"key"}).AddRow("members:read"))
	roles, total, err := repo.ListRoles(ctx, "org", 10, 0)
	if err != nil || total != 1 || len(roles) != 1 || len(roles[0].Permissions) != 1 {
		t.Fatalf("roles=%#v,%d,%v", roles, total, err)
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO organization_roles").WithArgs(anyArgs(4)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO organization_role_permissions").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()
	role, err := repo.CreateRole(ctx, "org", "custom", "Custom", []string{"members:read"})
	if err != nil || role.Key != "custom" {
		t.Fatalf("role=%#v,%v", role, err)
	}
}

func TestEnsureAnotherAdmin(t *testing.T) {
	ctx := context.Background()
	mock := mockPool(t)
	mock.ExpectQuery("SELECT COUNT").WithArgs("org", "m").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(1))
	if err := ensureAnotherAdmin(ctx, mock, "org", "m"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT COUNT").WithArgs("org", "m").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(0))
	if err := ensureAnotherAdmin(ctx, mock, "org", "m"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin=%v", err)
	}
	mock.ExpectQuery("SELECT COUNT").WithArgs("org", "m").WillReturnError(errors.New("db"))
	if err := ensureAnotherAdmin(ctx, mock, "org", "m"); err == nil {
		t.Fatal("expected query error")
	}
}

func TestMemberAdminAndRoleErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("demote admin", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewMemberRepository(mock)
		mock.ExpectBegin()
		mock.ExpectExec("SELECT pg_advisory").WithArgs("org").WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectQuery("SELECT m.person_id").WithArgs("m", "org").WillReturnRows(pgxmock.NewRows([]string{"person", "admin"}).AddRow("p", true))
		mock.ExpectQuery("SELECT EXISTS").WithArgs("org", "member").WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectQuery("SELECT COUNT").WithArgs("org", "m").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(0))
		mock.ExpectRollback()
		if err := repo.Update(ctx, "org", "m", "", nil, []string{"member"}); !errors.Is(err, ErrLastAdmin) {
			t.Fatalf("demote=%v", err)
		}
	})
	t.Run("delete admin", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewMemberRepository(mock)
		mock.ExpectBegin()
		mock.ExpectExec("SELECT pg_advisory").WithArgs("org").WillReturnResult(pgxmock.NewResult("SELECT", 1))
		mock.ExpectQuery("SELECT m.person_id").WithArgs("m", "org").WillReturnRows(pgxmock.NewRows([]string{"person", "admin"}).AddRow("p", true))
		mock.ExpectQuery("SELECT COUNT").WithArgs("org", "m").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(1))
		mock.ExpectExec("DELETE FROM persons").WithArgs("p").WillReturnResult(pgxmock.NewResult("DELETE", 1))
		mock.ExpectCommit()
		if err := repo.Delete(ctx, "org", "m"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("role conflict and permission", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewMemberRepository(mock)
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO organization_roles").WithArgs(anyArgs(4)...).WillReturnError(errors.New("uq_organization_roles"))
		mock.ExpectRollback()
		if _, err := repo.CreateRole(ctx, "org", "x", "X", nil); !errors.Is(err, ErrConflict) {
			t.Fatalf("conflict=%v", err)
		}
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO organization_roles").WithArgs(anyArgs(4)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("INSERT INTO organization_role_permissions").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("INSERT", 0))
		mock.ExpectRollback()
		if _, err := repo.CreateRole(ctx, "org", "x", "X", []string{"bad"}); err == nil {
			t.Fatal("unknown permission accepted")
		}
	})
}

func TestMemberCreateAndUpdateValidationErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("begin create", func(t *testing.T) {
		mock := mockPool(t)
		mock.ExpectBegin().WillReturnError(errors.New("begin"))
		err := NewMemberRepository(mock).Create(ctx, "org", &models.Member{
			Identities: []models.IdentityInput{{Type: "sms", Identifier: "1"}},
		})
		if err == nil {
			t.Fatal("begin error ignored")
		}
	})
	t.Run("person insert", func(t *testing.T) {
		mock := mockPool(t)
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO persons").WithArgs(anyArgs(2)...).WillReturnError(errors.New("insert"))
		mock.ExpectRollback()
		err := NewMemberRepository(mock).Create(ctx, "org", &models.Member{
			Identities: []models.IdentityInput{{Type: "sms", Identifier: "1"}},
		})
		if err == nil {
			t.Fatal("person error ignored")
		}
	})
	t.Run("identity conflict", func(t *testing.T) {
		mock := mockPool(t)
		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO persons").WithArgs(anyArgs(2)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("INSERT INTO organization_memberships").WithArgs(anyArgs(3)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		batch := mock.ExpectBatch()
		batch.ExpectExec("INSERT INTO login_identities").WithArgs(anyArgs(6)...).WillReturnError(errors.New("uq_login_identities"))
		mock.ExpectRollback()
		err := NewMemberRepository(mock).Create(ctx, "org", &models.Member{
			Identities: []models.IdentityInput{{Type: "sms", Identifier: "1"}},
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("conflict=%v", err)
		}
	})
	for _, tc := range []struct {
		name string
		roles []string
		setup func(pgxmock.PgxPoolIface)
		want error
	}{
		{"empty", []string{}, func(pgxmock.PgxPoolIface) {}, ErrInvalidRoleAssignment},
		{"blank", []string{" "}, func(pgxmock.PgxPoolIface) {}, ErrUnknownRole},
		{"unknown", []string{"missing"}, func(mock pgxmock.PgxPoolIface) {
			mock.ExpectQuery("SELECT EXISTS").WithArgs("org", "missing").WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
		}, ErrUnknownRole},
		{"duplicate", []string{"member", " MEMBER "}, func(mock pgxmock.PgxPoolIface) {
			mock.ExpectQuery("SELECT EXISTS").WithArgs("org", "member").WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
		}, ErrInvalidRoleAssignment},
	} {
		t.Run("update "+tc.name, func(t *testing.T) {
			mock := mockPool(t)
			mock.ExpectBegin()
			mock.ExpectExec("SELECT pg_advisory").WithArgs("org").WillReturnResult(pgxmock.NewResult("SELECT", 1))
			mock.ExpectQuery("SELECT m.person_id").WithArgs("m", "org").WillReturnRows(pgxmock.NewRows([]string{"person", "admin"}).AddRow("p", false))
			tc.setup(mock)
			mock.ExpectRollback()
			err := NewMemberRepository(mock).Update(ctx, "org", "m", "", nil, tc.roles)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestMemberReadAndRoleErrorPaths(t *testing.T) {
	ctx := context.Background()
	t.Run("list count", func(t *testing.T) {
		mock := mockPool(t)
		mock.ExpectQuery("SELECT COUNT").WithArgs("org").WillReturnError(errors.New("count"))
		if _, _, err := NewMemberRepository(mock).List(ctx, "org", 1, 0); err == nil {
			t.Fatal("count error ignored")
		}
	})
	t.Run("list query", func(t *testing.T) {
		mock := mockPool(t)
		mock.ExpectQuery("SELECT COUNT").WithArgs("org").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(0))
		mock.ExpectQuery("SELECT m.id").WithArgs("org", 1, 0).WillReturnError(errors.New("query"))
		if _, _, err := NewMemberRepository(mock).List(ctx, "org", 1, 0); err == nil {
			t.Fatal("list error ignored")
		}
	})
	t.Run("get query", func(t *testing.T) {
		mock := mockPool(t)
		mock.ExpectQuery("SELECT m.id").WithArgs("m", "org").WillReturnError(errors.New("query"))
		if _, err := NewMemberRepository(mock).Get(ctx, "org", "m"); err == nil {
			t.Fatal("get error ignored")
		}
	})
	t.Run("details stages", func(t *testing.T) {
		for stage := 0; stage < 3; stage++ {
			mock := mockPool(t)
			member := &models.Member{ID: "m", PersonID: "p"}
			if stage == 0 {
				mock.ExpectQuery("SELECT type").WithArgs("p").WillReturnError(errors.New("identity"))
			} else {
				mock.ExpectQuery("SELECT type").WithArgs("p").WillReturnRows(pgxmock.NewRows([]string{"type", "identifier"}))
				if stage == 1 {
					mock.ExpectQuery("SELECT r.key").WithArgs("m").WillReturnError(errors.New("role"))
				} else {
					mock.ExpectQuery("SELECT r.key").WithArgs("m").WillReturnRows(pgxmock.NewRows([]string{"key"}))
					mock.ExpectQuery("SELECT DISTINCT").WithArgs("m").WillReturnError(errors.New("permission"))
				}
			}
			if err := NewMemberRepository(mock).loadDetails(ctx, member); err == nil {
				t.Fatalf("stage %d error ignored", stage)
			}
		}
	})
	t.Run("list roles errors", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewMemberRepository(mock)
		mock.ExpectQuery("SELECT COUNT").WithArgs("org").WillReturnError(errors.New("count"))
		if _, _, err := repo.ListRoles(ctx, "org", 1, 0); err == nil {
			t.Fatal("role count error ignored")
		}
		mock.ExpectQuery("SELECT COUNT").WithArgs("org").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(0))
		mock.ExpectQuery("SELECT id").WithArgs("org", 1, 0).WillReturnError(errors.New("query"))
		if _, _, err := repo.ListRoles(ctx, "org", 1, 0); err == nil {
			t.Fatal("role query error ignored")
		}
		mock.ExpectQuery("SELECT COUNT").WithArgs("org").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(1))
		mock.ExpectQuery("SELECT id").WithArgs("org", 1, 0).WillReturnRows(pgxmock.NewRows([]string{"id", "key", "name", "system"}).AddRow("r", "x", "X", false))
		mock.ExpectQuery("SELECT permission_key").WithArgs("r").WillReturnError(errors.New("permission"))
		if _, _, err := repo.ListRoles(ctx, "org", 1, 0); err == nil {
			t.Fatal("permission query error ignored")
		}
	})
}
