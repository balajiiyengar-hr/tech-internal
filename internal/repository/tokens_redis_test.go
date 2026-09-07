package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"tech-internal/internal/models"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisTokenStore(t *testing.T) (*RedisTokenStore, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisTokenStore(client), client, server
}

func liveRecord(jti, family, kind string, ttl time.Duration) models.OAuthToken {
	return models.OAuthToken{
		JTI: jti, FamilyID: family, Domain: "acme", Type: "email",
		Identifier: "user@acme.test", Kind: kind, ExpiresAt: time.Now().Add(ttl),
	}
}

func TestRedisTokenStorePairLookupAndExpiry(t *testing.T) {
	ctx := context.Background()
	store, client, server := newRedisTokenStore(t)
	access := liveRecord("access-1", "family-1", models.TokenKindAccess, time.Minute)
	refresh := liveRecord("refresh-1", "family-1", models.TokenKindRefresh, time.Hour)

	if err := store.InsertPair(ctx, access, "unused", refresh, "unused"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetActiveByJTI(ctx, access.JTI)
	if err != nil || got.JTI != access.JTI || got.FamilyID != access.FamilyID {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	if ttl := client.TTL(ctx, tokenKey(access.JTI)).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("access TTL=%v", ttl)
	}

	server.FastForward(61 * time.Second)
	if _, err := store.GetActiveByJTI(ctx, access.JTI); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired access err=%v", err)
	}
	if _, err := store.GetActiveByJTI(ctx, refresh.JTI); err != nil {
		t.Fatalf("refresh should still be live: %v", err)
	}
}

func TestRedisTokenStoreRotateIsSingleUse(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newRedisTokenStore(t)
	oldAccess := liveRecord("access-old", "family-2", models.TokenKindAccess, time.Minute)
	oldRefresh := liveRecord("refresh-old", "family-2", models.TokenKindRefresh, time.Hour)
	if err := store.InsertPair(ctx, oldAccess, "", oldRefresh, ""); err != nil {
		t.Fatal(err)
	}

	newAccess := liveRecord("access-new", "family-2", models.TokenKindAccess, time.Minute)
	newRefresh := liveRecord("refresh-new", "family-2", models.TokenKindRefresh, time.Hour)
	ok, err := store.RotatePair(ctx, oldRefresh.JTI, newAccess, "", newRefresh, "")
	if err != nil || !ok {
		t.Fatalf("first rotate ok=%v err=%v", ok, err)
	}
	ok, err = store.RotatePair(ctx, oldRefresh.JTI,
		liveRecord("access-replay", "family-2", models.TokenKindAccess, time.Minute), "",
		liveRecord("refresh-replay", "family-2", models.TokenKindRefresh, time.Hour), "")
	if err != nil || ok {
		t.Fatalf("replay rotate ok=%v err=%v", ok, err)
	}
	if _, err := store.GetActiveByJTI(ctx, oldRefresh.JTI); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consumed refresh err=%v", err)
	}
	if _, err := store.GetActiveByJTI(ctx, newAccess.JTI); err != nil {
		t.Fatalf("new access missing: %v", err)
	}
}

func TestRedisTokenStoreRevokeFamily(t *testing.T) {
	ctx := context.Background()
	store, client, _ := newRedisTokenStore(t)
	access := liveRecord("access-3", "family-3", models.TokenKindAccess, time.Minute)
	refresh := liveRecord("refresh-3", "family-3", models.TokenKindRefresh, time.Hour)
	if err := store.InsertPair(ctx, access, "", refresh, ""); err != nil {
		t.Fatal(err)
	}

	count, err := store.RevokeFamily(ctx, access.FamilyID)
	if err != nil || count != 2 {
		t.Fatalf("revoke count=%d err=%v", count, err)
	}
	for _, jti := range []string{access.JTI, refresh.JTI} {
		if _, err := store.GetActiveByJTI(ctx, jti); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s remained active: %v", jti, err)
		}
	}
	if client.Exists(ctx, revokedFamilyKey(access.FamilyID)).Val() != 1 {
		t.Fatal("revocation marker missing")
	}

	ok, err := store.RotatePair(ctx, refresh.JTI,
		liveRecord("raced-access", access.FamilyID, models.TokenKindAccess, time.Minute), "",
		liveRecord("raced-refresh", access.FamilyID, models.TokenKindRefresh, time.Hour), "")
	if err != nil || ok {
		t.Fatalf("rotation recreated revoked family: ok=%v err=%v", ok, err)
	}
	if err := store.InsertPair(ctx,
		liveRecord("insert-access", access.FamilyID, models.TokenKindAccess, time.Minute), "",
		liveRecord("insert-refresh", access.FamilyID, models.TokenKindRefresh, time.Hour), "",
	); !errors.Is(err, ErrTokenFamilyRevoked) {
		t.Fatalf("insert into revoked family err=%v", err)
	}
}

func TestRedisTokenStoreRejectsInvalidAndCorruptRecords(t *testing.T) {
	ctx := context.Background()
	store, client, _ := newRedisTokenStore(t)
	valid := liveRecord("valid", "family-4", models.TokenKindAccess, time.Minute)
	tests := []models.OAuthToken{
		{FamilyID: "family", ExpiresAt: time.Now().Add(time.Minute)},
		{JTI: "jti", ExpiresAt: time.Now().Add(time.Minute)},
		{JTI: "jti", FamilyID: "family", ExpiresAt: time.Now().Add(-time.Minute)},
	}
	for _, token := range tests {
		if err := store.InsertPair(ctx, token, "", valid, ""); err == nil {
			t.Fatalf("accepted invalid token %#v", token)
		}
	}
	if err := store.InsertPair(ctx, valid, "",
		models.OAuthToken{JTI: "expired-refresh", FamilyID: "family-4", ExpiresAt: time.Now().Add(-time.Minute)}, "",
	); err == nil {
		t.Fatal("accepted invalid refresh token")
	}
	if ok, err := store.RotatePair(ctx, "old",
		models.OAuthToken{JTI: "expired-access", FamilyID: "family-4", ExpiresAt: time.Now().Add(-time.Minute)}, "",
		valid, "",
	); err == nil || ok {
		t.Fatalf("accepted invalid rotated access: ok=%v err=%v", ok, err)
	}
	if ok, err := store.RotatePair(ctx, "old", valid, "",
		models.OAuthToken{JTI: "expired-refresh", FamilyID: "family-4", ExpiresAt: time.Now().Add(-time.Minute)}, "",
	); err == nil || ok {
		t.Fatalf("accepted invalid rotated refresh: ok=%v err=%v", ok, err)
	}
	if err := client.Set(ctx, tokenKey("corrupt"), "{", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetActiveByJTI(ctx, "corrupt"); err == nil {
		t.Fatal("accepted corrupt token")
	}
	if count, err := store.RevokeFamily(ctx, "missing"); err != nil || count != 0 {
		t.Fatalf("missing family count=%d err=%v", count, err)
	}
}

func TestRedisTokenStorePropagatesRedisErrors(t *testing.T) {
	ctx := context.Background()
	store, client, _ := newRedisTokenStore(t)
	access := liveRecord("access-error", "family-error", models.TokenKindAccess, time.Minute)
	refresh := liveRecord("refresh-error", "family-error", models.TokenKindRefresh, time.Hour)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	if err := store.InsertPair(ctx, access, "", refresh, ""); err == nil {
		t.Fatal("insert ignored Redis error")
	}
	if ok, err := store.RotatePair(ctx, refresh.JTI, access, "", refresh, ""); err == nil || ok {
		t.Fatalf("rotate ignored Redis error: ok=%v err=%v", ok, err)
	}
	if _, err := store.GetActiveByJTI(ctx, access.JTI); err == nil {
		t.Fatal("lookup ignored Redis error")
	}
	if _, err := store.RevokeFamily(ctx, access.FamilyID); err == nil {
		t.Fatal("revoke ignored Redis error")
	}
	if ttlMillis(time.Nanosecond) != 1 {
		t.Fatal("sub-millisecond TTL was not clamped")
	}
	if maxDuration(time.Second, time.Minute) != time.Minute {
		t.Fatal("maxDuration returned the smaller duration")
	}
}
