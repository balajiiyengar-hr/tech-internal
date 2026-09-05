package otp

import (
	"context"
	"errors"
	"testing"
	"time"

	"tech-internal/internal/auth"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestStore(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	keyBytes := []byte("0123456789abcdef0123456789abcdef")
	store := NewStore(client, 0, keyBytes)
	if store.ttl != 120*time.Second {
		t.Fatalf("default ttl = %v", store.ttl)
	}
	store = NewStore(client, time.Minute, keyBytes)
	if store.ttl != 120*time.Second {
		t.Fatalf("configured ttl must remain 120s, got %v", store.ttl)
	}
	protected, err := auth.ProtectOTP(keyBytes, "123456")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Save(ctx, "Example.COM", "user", protected); err != nil {
		t.Fatal(err)
	}
	if !server.Exists(key("Example.COM", "user")) {
		t.Fatal("OTP was not saved")
	}
	if got := server.TTL(key("Example.COM", "user")); got != 120*time.Second {
		t.Fatalf("OTP redis ttl = %v", got)
	}
	if err := store.VerifyAndConsume(ctx, "Example.COM", "user", "000000"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("mismatch = %v", err)
	}
	if err := store.VerifyAndConsume(ctx, "Example.COM", "user", "123456"); !errors.Is(err, ErrExpired) {
		t.Fatalf("consumed OTP = %v", err)
	}
	if err := store.Save(ctx, "d", "u", protected); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyAndConsume(ctx, "d", "u", "123456"); err != nil {
		t.Fatal(err)
	}
	server.Set(key("d", "bad"), "not-sealed")
	if err := store.VerifyAndConsume(ctx, "d", "bad", "123456"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("invalid protected value = %v", err)
	}
	client.Close()
	if err := store.Save(ctx, "d", "u", protected); err == nil {
		t.Fatal("expected redis error")
	}
	if err := store.VerifyAndConsume(ctx, "d", "u", "123456"); err == nil {
		t.Fatal("expected redis error")
	}
}
