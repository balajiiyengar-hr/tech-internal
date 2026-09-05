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

func mockPool(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	return mock
}

func anyArgs(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = pgxmock.AnyArg()
	}
	return out
}

func TestUserRepository(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	t.Run("get", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewUserRepository(mock)
		mock.ExpectQuery("SELECT p.id").WithArgs("acme", "email", "a@b").
			WillReturnRows(pgxmock.NewRows([]string{"p", "m", "i", "o", "d", "t", "ident", "pw", "role", "roles", "perms", "active", "name", "created"}).
				AddRow("p", "m", "i", "o", "acme", "email", "a@b", nil, "admin", []string{"org_admin"}, []string{"*"}, true, "A", now))
		user, err := repo.Get(ctx, "acme", "email", "a@b")
		if err != nil || user.MembershipID != "m" || user.PasswordHash != nil {
			t.Fatalf("Get=%#v,%v", user, err)
		}
		mock.ExpectQuery("SELECT p.id").WithArgs(anyArgs(3)...).WillReturnError(pgx.ErrNoRows)
		if _, err := repo.Get(ctx, "x", "x", "x"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("not found=%v", err)
		}
		mock.ExpectQuery("SELECT p.id").WithArgs(anyArgs(3)...).WillReturnError(errors.New("db"))
		if _, err := repo.Get(ctx, "x", "x", "x"); err == nil {
			t.Fatal("expected db error")
		}
	})

	t.Run("list", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewUserRepository(mock)
		mock.ExpectQuery("SELECT COUNT").WithArgs("acme").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(1))
		mock.ExpectQuery("WITH selected_memberships").WithArgs("acme", 10, 0).
			WillReturnRows(pgxmock.NewRows([]string{"p", "m", "i", "o", "d", "t", "ident", "role", "roles", "perms", "active", "name", "created"}).
				AddRow("p", "m", "i", "o", "acme", "email", "a@b", "admin", []string{"org_admin"}, []string{"*"}, true, "A", now))
		users, total, err := repo.ListByDomain(ctx, "acme", 10, 0)
		if err != nil || total != 1 || len(users) != 1 {
			t.Fatalf("List=%#v,%d,%v", users, total, err)
		}
		mock.ExpectQuery("SELECT COUNT").WithArgs(anyArgs(1)...).WillReturnError(errors.New("count"))
		if _, _, err := repo.ListByDomain(ctx, "x", 1, 0); err == nil {
			t.Fatal("expected count error")
		}
		mock.ExpectQuery("SELECT COUNT").WithArgs(anyArgs(1)...).WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(0))
		mock.ExpectQuery("WITH selected_memberships").WithArgs(anyArgs(3)...).WillReturnError(errors.New("query"))
		if _, _, err := repo.ListByDomain(ctx, "x", 1, 0); err == nil {
			t.Fatal("expected query error")
		}
	})

	t.Run("create update", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewUserRepository(mock)
		mock.ExpectExec("WITH org").WithArgs("acme", pgxmock.AnyArg(), "A", pgxmock.AnyArg(), true, pgxmock.AnyArg(), "email", "a@b", pgxmock.AnyArg(), "org_admin").
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		if err := repo.Create(ctx, models.User{Domain: "acme", DisplayName: "A", Active: true, Type: "email", Identifier: "a@b", Role: "admin"}, nil); err != nil {
			t.Fatal(err)
		}
		mock.ExpectExec("WITH org").WithArgs(anyArgs(10)...).WillReturnResult(pgxmock.NewResult("INSERT", 0))
		if err := repo.Create(ctx, models.User{Role: "missing"}, nil); err == nil {
			t.Fatal("expected unknown role")
		}
		mock.ExpectExec("WITH org").WithArgs(anyArgs(10)...).WillReturnError(errors.New("uq_login_identities"))
		if err := repo.Create(ctx, models.User{}, nil); !errors.Is(err, ErrConflict) {
			t.Fatalf("conflict=%v", err)
		}
		mock.ExpectExec("UPDATE login_identities").WithArgs(anyArgs(4)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		if err := repo.UpdatePasswordHash(ctx, "d", "email", "i", "h"); err != nil {
			t.Fatal(err)
		}
		mock.ExpectExec("UPDATE login_identities").WithArgs(anyArgs(4)...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		if err := repo.UpdatePasswordHash(ctx, "d", "email", "i", "h"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("update not found=%v", err)
		}
	})
}

func TestDomainAndAppRepositories(t *testing.T) {
	ctx := context.Background()
	mock := mockPool(t)
	domains := NewDomainRepository(mock)
	apps := NewAppRepository(mock)
	mock.ExpectQuery("SELECT domain").WithArgs("acme").WillReturnRows(pgxmock.NewRows([]string{"domain", "email", "sms"}).AddRow("acme", true, false))
	domain, err := domains.Get(ctx, "acme")
	if err != nil || !domain.EmailLoginEnabled {
		t.Fatalf("domain=%#v,%v", domain, err)
	}
	mock.ExpectQuery("SELECT domain").WithArgs(anyArgs(1)...).WillReturnError(pgx.ErrNoRows)
	if _, err := domains.Get(ctx, "none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("domain missing=%v", err)
	}
	mock.ExpectExec("UPDATE domains").WithArgs(anyArgs(3)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := domains.Update(ctx, *domain); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE domains").WithArgs(anyArgs(3)...).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	if err := domains.Update(ctx, *domain); !errors.Is(err, ErrNotFound) {
		t.Fatalf("domain update=%v", err)
	}

	mock.ExpectQuery("SELECT COUNT").WithArgs("acme").WillReturnRows(pgxmock.NewRows([]string{"n"}).AddRow(1))
	mock.ExpectQuery("SELECT id").WithArgs("acme", 10, 0).
		WillReturnRows(pgxmock.NewRows([]string{"id", "domain", "section", "name", "url", "icon", "sort", "active"}).
			AddRow("id", "acme", "S", "App", "url", "", 1, true))
	list, total, err := apps.ListByDomain(ctx, "acme", 10, 0)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("apps=%#v,%d,%v", list, total, err)
	}
	app := &models.PortalApp{Domain: "acme", Name: "A", Active: true}
	mock.ExpectExec("INSERT INTO portal_apps").WithArgs(anyArgs(8)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := apps.Create(ctx, app); err != nil || app.ID == "" {
		t.Fatalf("create=%#v,%v", app, err)
	}
	mock.ExpectExec("DELETE FROM portal_apps").WithArgs(anyArgs(1)...).WillReturnResult(pgxmock.NewResult("DELETE", 1))
	if err := apps.Delete(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("DELETE FROM portal_apps").WithArgs(anyArgs(1)...).WillReturnResult(pgxmock.NewResult("DELETE", 0))
	if err := apps.Delete(ctx, app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing=%v", err)
	}
}
