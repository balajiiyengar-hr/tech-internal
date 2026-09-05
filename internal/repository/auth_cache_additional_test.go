package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"tech-internal/internal/models"

	"github.com/alicebob/miniredis/v2"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/redis/go-redis/v9"
)

func TestAuthCacheMissesAndPasswordlessEntries(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	cache := NewAuthCache(client, 0)

	if cache.maxEntries != defaultAuthCacheEntries {
		t.Fatalf("default max entries = %d", cache.maxEntries)
	}
	if err := cache.Put(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := cache.InvalidatePerson(ctx, " "); err != nil {
		t.Fatal(err)
	}

	user := &models.User{
		PersonID: "passwordless", Domain: "acme", Type: "email", Identifier: " User@Example.com ",
	}
	if err := cache.Put(ctx, user); err != nil {
		t.Fatal(err)
	}
	got, ok := cache.Get(ctx, "ACME", "EMAIL", "user@example.com")
	if !ok || got.PasswordHash != nil {
		t.Fatalf("passwordless cache get = %#v, %v", got, ok)
	}
	if _, ok := cache.Get(ctx, "other", "email", "user@example.com"); ok {
		t.Fatal("domain mismatch returned a cache hit")
	}

	member := cacheMember("email", "broken@example.com")
	if err := client.Set(ctx, identityCacheKey(member), "{bad json", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get(ctx, "acme", "email", "broken@example.com"); ok {
		t.Fatal("malformed cache entry returned a hit")
	}

	hash := "protected"
	withPassword := &models.User{
		PersonID: "missing-password", Domain: "acme", Type: "email",
		Identifier: "missing@example.com", PasswordHash: &hash,
	}
	if err := cache.Put(ctx, withPassword); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, passwordCacheKey(cacheMember("email", "missing@example.com"))).Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get(ctx, "acme", "email", "missing@example.com"); ok {
		t.Fatal("identity without its password key returned a hit")
	}

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Get(ctx, "acme", "email", "anything@example.com"); ok {
		t.Fatal("closed Redis client returned a hit")
	}
}

func TestDBLimiterDefaultsAndCancellation(t *testing.T) {
	limiter := NewDBLimiter(0)
	if cap(limiter.slots) != 40 {
		t.Fatalf("default limiter capacity = %d", cap(limiter.slots))
	}
	called := false
	if err := limiter.run(context.Background(), func() error {
		called = true
		return nil
	}); err != nil || !called {
		t.Fatalf("successful run: called=%v err=%v", called, err)
	}

	blocked := NewDBLimiter(1)
	blocked.slots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := blocked.run(ctx, func() error {
		t.Fatal("canceled limiter ran callback")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run error = %v", err)
	}
}

func TestCachedUserRepositoryAuxiliaryOperations(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	mock := mockPool(t)
	repo := NewCachedUserRepository(NewUserRepository(mock), NewAuthCache(client, 10), nil)
	now := time.Now()

	mock.ExpectQuery("SELECT COUNT").WithArgs("acme").WillReturnRows(
		pgxmock.NewRows([]string{"count"}).AddRow(int64(1)),
	)
	mock.ExpectQuery("WITH selected_memberships").WithArgs("acme", 10, 0).WillReturnRows(
		pgxmock.NewRows([]string{
			"p", "m", "i", "o", "d", "t", "ident", "role", "roles", "perms", "active", "name", "created",
		}).AddRow("person", "membership", "identity", "org", "acme", "email", "a@b",
			"member", []string{"member"}, []string{"apps:read"}, true, "A", now),
	)
	users, total, err := repo.ListByDomain(ctx, "acme", 10, 0)
	if err != nil || total != 1 || len(users) != 1 {
		t.Fatalf("list: users=%#v total=%d err=%v", users, total, err)
	}

	hash := "protected"
	mock.ExpectQuery("SELECT p.id").WithArgs("org", "email", "a@b").
		WillReturnRows(testCachedUserRow(now, &hash))
	if err := repo.loadByOrganizationAndPut(ctx, "org", "email", "a@b"); err != nil {
		t.Fatal(err)
	}
	if got, ok := repo.cache.Get(ctx, "acme", "email", "a@b"); !ok || got.PersonID != "person" {
		t.Fatalf("organization fill = %#v, %v", got, ok)
	}
	if err := repo.invalidatePerson(ctx, "person"); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.cache.Get(ctx, "acme", "email", "a@b"); ok {
		t.Fatal("person invalidation left an entry")
	}
}

func TestCachedDomainRepositoryReadAndWriteThrough(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	mock := mockPool(t)
	repo := NewCachedDomainRepository(NewDomainRepository(mock), client, nil)

	mock.ExpectQuery("SELECT domain").WithArgs("acme").WillReturnRows(
		pgxmock.NewRows([]string{"domain", "email", "sms"}).AddRow("acme", true, false),
	)
	got, err := repo.Get(ctx, " AcMe ")
	if err != nil || got.Domain != "acme" || !got.EmailLoginEnabled {
		t.Fatalf("cache miss fill = %#v, %v", got, err)
	}
	got, err = repo.Get(ctx, "ACME")
	if err != nil || got.Domain != "acme" {
		t.Fatalf("cache hit = %#v, %v", got, err)
	}

	updated := models.DomainSettings{Domain: "acme", EmailLoginEnabled: false, SMSLoginEnabled: true}
	mock.ExpectExec("UPDATE domains").WithArgs("acme", false, true).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := repo.Update(ctx, updated); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, "acme")
	if err != nil || got.EmailLoginEnabled || !got.SMSLoginEnabled {
		t.Fatalf("updated cache = %#v, %v", got, err)
	}
}

func TestCachedRepositoriesPropagateDatabaseAndCacheErrors(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	mock := mockPool(t)
	users := NewCachedUserRepository(NewUserRepository(mock), NewAuthCache(client, 10), NewDBLimiter(1))
	wantErr := errors.New("database unavailable")

	mock.ExpectQuery("SELECT p.id").WithArgs("acme", "email", "missing@acme.test").
		WillReturnError(wantErr)
	if _, err := users.Get(ctx, "acme", "email", "missing@acme.test"); !errors.Is(err, wantErr) {
		t.Fatalf("get error = %v", err)
	}
	mock.ExpectExec("WITH org").WithArgs("acme", pgxmock.AnyArg(), "", pgxmock.AnyArg(), false,
		pgxmock.AnyArg(), "email", "", pgxmock.AnyArg(), "member").WillReturnError(wantErr)
	if err := users.Create(ctx, models.User{Domain: "acme", Type: "email"}, nil); !errors.Is(err, wantErr) {
		t.Fatalf("create error = %v", err)
	}
	mock.ExpectExec("UPDATE login_identities").WithArgs("acme", "email", "a@b", "hash").
		WillReturnError(wantErr)
	if err := users.UpdatePasswordHash(ctx, "acme", "email", "a@b", "hash"); !errors.Is(err, wantErr) {
		t.Fatalf("password update error = %v", err)
	}
	mock.ExpectQuery("SELECT p.id").WithArgs("acme", "email", "load@acme.test").
		WillReturnError(wantErr)
	if err := users.loadAndPut(ctx, "acme", "email", "load@acme.test"); !errors.Is(err, wantErr) {
		t.Fatalf("load error = %v", err)
	}
	mock.ExpectQuery("SELECT p.id").WithArgs("org", "email", "org@acme.test").
		WillReturnError(wantErr)
	if err := users.loadByOrganizationAndPut(ctx, "org", "email", "org@acme.test"); !errors.Is(err, wantErr) {
		t.Fatalf("organization load error = %v", err)
	}

	domains := NewCachedDomainRepository(NewDomainRepository(mock), client, NewDBLimiter(1))
	if err := client.Set(ctx, domainCacheKey("broken"), "{bad json", 0).Err(); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT domain").WithArgs("broken").WillReturnError(wantErr)
	if _, err := domains.Get(ctx, "broken"); !errors.Is(err, wantErr) {
		t.Fatalf("domain get error = %v", err)
	}
	mock.ExpectExec("UPDATE domains").WithArgs("acme", false, false).WillReturnError(wantErr)
	if err := domains.Update(ctx, models.DomainSettings{Domain: "acme"}); !errors.Is(err, wantErr) {
		t.Fatalf("domain update error = %v", err)
	}
}

func TestCachedRepositoriesPropagateRedisWriteErrors(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	mock := mockPool(t)
	users := NewCachedUserRepository(NewUserRepository(mock), NewAuthCache(client, 10), NewDBLimiter(1))
	now := time.Now()
	hash := "protected"

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT p.id").WithArgs("acme", "email", "a@b").
		WillReturnRows(testCachedUserRow(now, &hash))
	if _, err := users.Get(ctx, "acme", "email", "a@b"); err == nil {
		t.Fatal("cache fill unexpectedly succeeded with closed Redis")
	}

	domains := NewCachedDomainRepository(NewDomainRepository(mock), client, NewDBLimiter(1))
	mock.ExpectQuery("SELECT domain").WithArgs("acme").WillReturnRows(
		pgxmock.NewRows([]string{"domain", "email", "sms"}).AddRow("acme", true, false),
	)
	if _, err := domains.Get(ctx, "acme"); err == nil {
		t.Fatal("domain cache fill unexpectedly succeeded with closed Redis")
	}
}

func TestMemberRepositoryWithCacheStopsBeforeUpdateOnLookupError(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	mock := mockPool(t)
	cachedUsers := NewCachedUserRepository(
		NewUserRepository(mock), NewAuthCache(client, 10), NewDBLimiter(1),
	)
	members := NewMemberRepository(mock, cachedUsers)
	wantErr := errors.New("member lookup failed")

	mock.ExpectQuery("SELECT m.id").WithArgs("membership", "org").WillReturnError(wantErr)
	if err := members.Update(ctx, "org", "membership", "", nil, nil); !errors.Is(err, wantErr) {
		t.Fatalf("member update error = %v", err)
	}
}
