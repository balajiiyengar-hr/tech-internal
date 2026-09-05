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

func tokenRecord(kind, jti string) models.OAuthToken {
	return models.OAuthToken{
		JTI: jti, FamilyID: "family", Domain: "acme", Type: "email",
		Identifier: "a@b", Kind: kind, ExpiresAt: time.Now().Add(time.Hour),
	}
}

func expectTokenBatch(mock pgxmock.PgxPoolIface) {
	batch := mock.ExpectBatch()
	batch.ExpectExec("INSERT INTO oauth_tokens").WithArgs(anyArgs(12)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	batch.ExpectExec("INSERT INTO oauth_tokens").WithArgs(anyArgs(12)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
}

func TestTokenRepository(t *testing.T) {
	ctx := context.Background()
	t.Run("insert and pair", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewTokenRepository(mock)
		mock.ExpectExec("INSERT INTO oauth_tokens").WithArgs(anyArgs(12)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		if err := repo.Insert(ctx, tokenRecord("access", "a"), "hash"); err != nil {
			t.Fatal(err)
		}
		mock.ExpectBegin()
		expectTokenBatch(mock)
		mock.ExpectCommit()
		if err := repo.InsertPair(ctx, tokenRecord("access", "a2"), "ah", tokenRecord("refresh", "r2"), "rh"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("rotate", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewTokenRepository(mock)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE oauth_tokens").WithArgs("old").WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		mock.ExpectRollback()
		ok, err := repo.RotatePair(ctx, "old", tokenRecord("access", "a"), "ah", tokenRecord("refresh", "r"), "rh")
		if err != nil || ok {
			t.Fatalf("not rotated=%v,%v", ok, err)
		}
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE oauth_tokens").WithArgs("old").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		expectTokenBatch(mock)
		mock.ExpectCommit()
		ok, err = repo.RotatePair(ctx, "old", tokenRecord("access", "a"), "ah", tokenRecord("refresh", "r"), "rh")
		if err != nil || !ok {
			t.Fatalf("rotated=%v,%v", ok, err)
		}
	})

	t.Run("read and revoke", func(t *testing.T) {
		mock := mockPool(t)
		repo := NewTokenRepository(mock)
		now := time.Now()
		columns := []string{"id", "jti", "family", "domain", "type", "identifier", "kind", "expires", "revoked", "created", "membership", "identity", "organization"}
		mock.ExpectQuery("SELECT id").WithArgs("jti").WillReturnRows(pgxmock.NewRows(columns).
			AddRow("id", "jti", "f", "d", "email", "a@b", "access", now.Add(time.Hour), nil, now, "", "", ""))
		rec, err := repo.GetActiveByJTI(ctx, "jti")
		if err != nil || rec.ID != "id" {
			t.Fatalf("get=%#v,%v", rec, err)
		}
		mock.ExpectQuery("SELECT id").WithArgs(anyArgs(1)...).WillReturnError(pgx.ErrNoRows)
		if _, err := repo.GetActiveByJTI(ctx, "none"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing=%v", err)
		}
		for _, call := range []struct {
			sql  string
			args []any
			fn   func() (int64, error)
		}{
			{"UPDATE oauth_tokens", []any{"j"}, func() (int64, error) { return repo.RevokeByJTI(ctx, "j") }},
			{"UPDATE oauth_tokens", []any{"f"}, func() (int64, error) { return repo.RevokeFamily(ctx, "f") }},
			{"UPDATE oauth_tokens", []any{"d", "email", "i"}, func() (int64, error) { return repo.RevokeUser(ctx, "d", "email", "i") }},
		} {
			mock.ExpectExec(call.sql).WithArgs(call.args...).WillReturnResult(pgxmock.NewResult("UPDATE", 2))
			n, err := call.fn()
			if err != nil || n != 2 {
				t.Fatalf("revoke=%d,%v", n, err)
			}
		}
		mock.ExpectQuery("SELECT id").WithArgs("d", "email", "i").WillReturnRows(pgxmock.NewRows(columns).
			AddRow("id", "jti", "f", "d", "email", "i", "access", now.Add(time.Hour), nil, now, "", "", ""))
		list, err := repo.ListActiveByUser(ctx, "d", "email", "i")
		if err != nil || len(list) != 1 {
			t.Fatalf("list=%#v,%v", list, err)
		}
		mock.ExpectQuery("SELECT id").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows(columns))
		list, err = repo.ListActiveByUser(ctx, "d", "email", "none")
		if err != nil || list == nil || len(list) != 0 {
			t.Fatalf("empty=%#v,%v", list, err)
		}
	})
}

func TestTokenRepositoryErrors(t *testing.T) {
	ctx := context.Background()
	mock := mockPool(t)
	repo := NewTokenRepository(mock)
	mock.ExpectExec("INSERT INTO oauth_tokens").WithArgs(anyArgs(12)...).WillReturnError(errors.New("db"))
	if err := repo.Insert(ctx, tokenRecord("access", "a"), "h"); err == nil {
		t.Fatal("insert error ignored")
	}
	mock.ExpectBegin().WillReturnError(errors.New("begin"))
	if err := repo.InsertPair(ctx, tokenRecord("access", "a"), "h", tokenRecord("refresh", "r"), "h"); err == nil {
		t.Fatal("pair begin error ignored")
	}
	mock.ExpectBegin().WillReturnError(errors.New("begin"))
	if _, err := repo.RotatePair(ctx, "old", tokenRecord("access", "a"), "h", tokenRecord("refresh", "r"), "h"); err == nil {
		t.Fatal("rotate begin error ignored")
	}
	for _, tc := range []struct {
		n    int
		call func() (int64, error)
	}{
		{1, func() (int64, error) { return repo.RevokeByJTI(ctx, "j") }},
		{1, func() (int64, error) { return repo.RevokeFamily(ctx, "f") }},
		{3, func() (int64, error) { return repo.RevokeUser(ctx, "d", "t", "i") }},
	} {
		mock.ExpectExec("UPDATE oauth_tokens").WithArgs(anyArgs(tc.n)...).WillReturnError(errors.New("db"))
		if _, err := tc.call(); err == nil {
			t.Fatal("revoke error ignored")
		}
	}
	mock.ExpectQuery("SELECT id").WithArgs(anyArgs(3)...).WillReturnError(errors.New("db"))
	if _, err := repo.ListActiveByUser(ctx, "d", "t", "i"); err == nil {
		t.Fatal("list error ignored")
	}
}

func TestTokenBatchFailures(t *testing.T) {
	ctx := context.Background()
	for _, second := range []bool{false, true} {
		t.Run(map[bool]string{false: "first", true: "second"}[second], func(t *testing.T) {
			mock := mockPool(t)
			repo := NewTokenRepository(mock)
			mock.ExpectBegin()
			batch := mock.ExpectBatch()
			if second {
				batch.ExpectExec("INSERT INTO oauth_tokens").WithArgs(anyArgs(12)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
			}
			batch.ExpectExec("INSERT INTO oauth_tokens").WithArgs(anyArgs(12)...).WillReturnError(errors.New("insert"))
			if !second {
				batch.ExpectExec("INSERT INTO oauth_tokens").WithArgs(anyArgs(12)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
			}
			mock.ExpectRollback()
			if err := repo.InsertPair(ctx, tokenRecord("access", "a"), "h", tokenRecord("refresh", "r"), "h"); err == nil {
				t.Fatal("batch error ignored")
			}
		})
	}
	t.Run("rotate update", func(t *testing.T) {
		mock := mockPool(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE oauth_tokens").WithArgs("old").WillReturnError(errors.New("update"))
		mock.ExpectRollback()
		if _, err := NewTokenRepository(mock).RotatePair(ctx, "old", tokenRecord("access", "a"), "h", tokenRecord("refresh", "r"), "h"); err == nil {
			t.Fatal("update error ignored")
		}
	})
	t.Run("rotate commit", func(t *testing.T) {
		mock := mockPool(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE oauth_tokens").WithArgs("old").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		expectTokenBatch(mock)
		mock.ExpectCommit().WillReturnError(errors.New("commit"))
		mock.ExpectRollback()
		if _, err := NewTokenRepository(mock).RotatePair(ctx, "old", tokenRecord("access", "a"), "h", tokenRecord("refresh", "r"), "h"); err == nil {
			t.Fatal("commit error ignored")
		}
	})
}
