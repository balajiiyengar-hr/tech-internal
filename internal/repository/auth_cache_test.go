package repository

import (
	"context"
	"testing"
	"time"

	"tech-internal/internal/models"

	"github.com/alicebob/miniredis/v2"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/redis/go-redis/v9"
)

func testCachedUserRow(now time.Time, hash *string) *pgxmock.Rows {
	return pgxmock.NewRows([]string{
		"p", "m", "i", "o", "d", "t", "ident", "pw", "role", "roles", "perms", "active", "name", "created",
	}).AddRow("person", "membership", "identity", "org", "acme", "email", "a@b", hash,
		"member", []string{"member"}, []string{"apps:read"}, true, "A", now)
}

func TestAuthCachePasswordAndLRU(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	cache := NewAuthCache(client, 2)
	hash := "enc:v1:protected-hash"

	clock := int64(100)
	oldClock := redisTime
	redisTime = func() int64 { clock++; return clock }
	t.Cleanup(func() { redisTime = oldClock })

	users := []*models.User{
		{PersonID: "p1", Domain: "acme", Type: "email", Identifier: "one@example.com", PasswordHash: &hash},
		{PersonID: "p2", Domain: "acme", Type: "email", Identifier: "two@example.com", PasswordHash: &hash},
		{PersonID: "p3", Domain: "acme", Type: "email", Identifier: "three@example.com", PasswordHash: &hash},
	}
	for _, user := range users {
		if err := cache.Put(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	if got := client.ZCard(ctx, "auth:lru").Val(); got != 2 {
		t.Fatalf("LRU size = %d", got)
	}
	if _, ok := cache.Get(ctx, "acme", "email", "one@example.com"); ok {
		t.Fatal("oldest cache entry was not evicted")
	}
	got, ok := cache.Get(ctx, "acme", "email", "three@example.com")
	if !ok || got.PasswordHash == nil || *got.PasswordHash != hash {
		t.Fatalf("password cache get = %#v, %v", got, ok)
	}
	member := cacheMember("email", "three@example.com")
	if ttl := client.TTL(ctx, passwordCacheKey(member)).Val(); ttl != -1 {
		t.Fatalf("password cache TTL = %v, want no expiry", ttl)
	}
	if err := cache.InvalidatePerson(ctx, "p3"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get(ctx, "acme", "email", "three@example.com"); ok {
		t.Fatal("deleted user remained cached")
	}
}

func TestCachedUserRepositoryMissFillAndPasswordWriteThrough(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	mock := mockPool(t)
	base := NewUserRepository(mock)
	repo := NewCachedUserRepository(base, NewAuthCache(client, 5000), NewDBLimiter(2))
	now := time.Now()
	hash := "enc:v1:first"

	mock.ExpectQuery("SELECT p.id").WithArgs("acme", "email", "a@b").
		WillReturnRows(testCachedUserRow(now, &hash))
	user, err := repo.Get(ctx, "acme", "email", "a@b")
	if err != nil || user.PasswordHash == nil || *user.PasswordHash != hash {
		t.Fatalf("cache miss fill = %#v, %v", user, err)
	}
	// No second Postgres expectation: this must be a Redis hit.
	if _, err := repo.Get(ctx, "acme", "email", "a@b"); err != nil {
		t.Fatal(err)
	}

	updated := "enc:v1:updated"
	mock.ExpectExec("UPDATE login_identities").WithArgs("acme", "email", "a@b", updated).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("SELECT p.id").WithArgs("acme", "email", "a@b").
		WillReturnRows(testCachedUserRow(now, &updated))
	if err := repo.UpdatePasswordHash(ctx, "acme", "email", "a@b", updated); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "acme", "email", "a@b")
	if err != nil || got.PasswordHash == nil || *got.PasswordHash != updated {
		t.Fatalf("write-through value = %#v, %v", got, err)
	}

	createdHash := "enc:v1:created"
	mock.ExpectExec("WITH org").WithArgs("acme", pgxmock.AnyArg(), "New", pgxmock.AnyArg(), true,
		pgxmock.AnyArg(), "email", "new@acme.test", &createdHash, "member").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT p.id").WithArgs("acme", "email", "new@acme.test").
		WillReturnRows(pgxmock.NewRows([]string{
			"p", "m", "i", "o", "d", "t", "ident", "pw", "role", "roles", "perms", "active", "name", "created",
		}).AddRow("new-person", "new-membership", "new-identity", "org", "acme", "email",
			"new@acme.test", &createdHash, "member", []string{"member"}, []string{}, true, "New", now))
	if err := repo.Create(ctx, models.User{
		Domain: "acme", Type: "email", Identifier: "new@acme.test",
		DisplayName: "New", Active: true,
	}, &createdHash); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Get(ctx, "acme", "email", "new@acme.test"); err != nil ||
		got.PasswordHash == nil || *got.PasswordHash != createdHash {
		t.Fatalf("create write-through = %#v, %v", got, err)
	}
}
