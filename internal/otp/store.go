package otp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"tech-internal/internal/auth"

	"github.com/redis/go-redis/v9"
)

var (
	ErrExpired  = errors.New("otp expired")
	ErrMismatch = errors.New("otp mismatch")
)

type Store struct {
	client *redis.Client
	ttl    time.Duration
	key    []byte
}

func NewStore(client *redis.Client, ttl time.Duration, encryptionKey []byte) *Store {
	// SMS OTP lifetime is a product/security invariant, not a deployment knob.
	return &Store{client: client, ttl: 120 * time.Second, key: encryptionKey}
}

func key(domain, identifier string) string {
	return fmt.Sprintf("otp:%s:%s", domain, identifier)
}

func (s *Store) Save(ctx context.Context, domain, identifier, protected string) error {
	return s.client.Set(ctx, key(domain, identifier), protected, s.ttl).Err()
}

func (s *Store) VerifyAndConsume(ctx context.Context, domain, identifier, otp string) error {
	// GETDEL makes each OTP single-use atomically, including concurrent verifies.
	stored, err := s.client.GetDel(ctx, key(domain, identifier)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrExpired
		}
		return err
	}

	expected, err := auth.OpenOTP(s.key, stored)
	if err != nil {
		return ErrMismatch
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(otp)) != 1 {
		return ErrMismatch
	}
	return nil
}
