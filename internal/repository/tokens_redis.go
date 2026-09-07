package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tech-internal/internal/models"

	"github.com/redis/go-redis/v9"
)

// ErrTokenFamilyRevoked prevents a token rotation racing with logout from
// recreating a session after its family has been revoked.
var ErrTokenFamilyRevoked = errors.New("token family revoked")

// RedisTokenStore is the source of truth for live access and refresh tokens.
// Token keys expire with their JWTs; no database lookup or write is required on
// login, refresh, logout, or authenticated requests.
type RedisTokenStore struct {
	client *redis.Client
}

func NewRedisTokenStore(client *redis.Client) *RedisTokenStore {
	return &RedisTokenStore{client: client}
}

func tokenKey(jti string) string          { return "oauth:token:" + jti }
func tokenFamilyKey(family string) string { return "oauth:family:" + family }
func revokedFamilyKey(family string) string {
	return "oauth:revoked-family:" + family
}

var insertTokenPairScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[4]) == 1 then
	return 0
end
redis.call("PSETEX", KEYS[1], ARGV[2], ARGV[1])
redis.call("PSETEX", KEYS[2], ARGV[4], ARGV[3])
redis.call("SADD", KEYS[3], KEYS[1], KEYS[2])
redis.call("PEXPIRE", KEYS[3], ARGV[5])
return 1
`)

var rotateTokenPairScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[5]) == 1 then
	return 0
end
if redis.call("EXISTS", KEYS[1]) == 0 then
	return 0
end
redis.call("DEL", KEYS[1])
redis.call("SREM", KEYS[4], KEYS[1])
redis.call("PSETEX", KEYS[2], ARGV[2], ARGV[1])
redis.call("PSETEX", KEYS[3], ARGV[4], ARGV[3])
redis.call("SADD", KEYS[4], KEYS[2], KEYS[3])
redis.call("PEXPIRE", KEYS[4], ARGV[5])
return 1
`)

var getActiveTokenScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[2]) == 1 then
	return false
end
return redis.call("GET", KEYS[1])
`)

var revokeTokenFamilyScript = redis.NewScript(`
local ttl = redis.call("PTTL", KEYS[1])
if ttl <= 0 then
	return 0
end
redis.call("PSETEX", KEYS[2], ttl, "1")
local tokens = redis.call("SMEMBERS", KEYS[1])
local count = 0
for _, token in ipairs(tokens) do
	count = count + redis.call("DEL", token)
end
redis.call("DEL", KEYS[1])
return count
`)

func (r *RedisTokenStore) InsertPair(
	ctx context.Context,
	access models.OAuthToken,
	_ string,
	refresh models.OAuthToken,
	_ string,
) error {
	accessJSON, accessTTL, err := liveToken(access)
	if err != nil {
		return err
	}
	refreshJSON, refreshTTL, err := liveToken(refresh)
	if err != nil {
		return err
	}
	familyTTL := maxDuration(accessTTL, refreshTTL)
	inserted, err := insertTokenPairScript.Run(ctx, r.client, []string{
		tokenKey(access.JTI),
		tokenKey(refresh.JTI),
		tokenFamilyKey(access.FamilyID),
		revokedFamilyKey(access.FamilyID),
	}, accessJSON, ttlMillis(accessTTL), refreshJSON, ttlMillis(refreshTTL), ttlMillis(familyTTL)).Int64()
	if err != nil {
		return err
	}
	if inserted != 1 {
		return ErrTokenFamilyRevoked
	}
	return nil
}

func (r *RedisTokenStore) RotatePair(
	ctx context.Context,
	oldRefreshJTI string,
	access models.OAuthToken,
	_ string,
	refresh models.OAuthToken,
	_ string,
) (bool, error) {
	accessJSON, accessTTL, err := liveToken(access)
	if err != nil {
		return false, err
	}
	refreshJSON, refreshTTL, err := liveToken(refresh)
	if err != nil {
		return false, err
	}
	familyTTL := maxDuration(accessTTL, refreshTTL)
	rotated, err := rotateTokenPairScript.Run(ctx, r.client, []string{
		tokenKey(oldRefreshJTI),
		tokenKey(access.JTI),
		tokenKey(refresh.JTI),
		tokenFamilyKey(access.FamilyID),
		revokedFamilyKey(access.FamilyID),
	}, accessJSON, ttlMillis(accessTTL), refreshJSON, ttlMillis(refreshTTL), ttlMillis(familyTTL)).Int64()
	if err != nil {
		return false, err
	}
	return rotated == 1, nil
}

func (r *RedisTokenStore) GetActiveByJTI(ctx context.Context, jti string) (*models.OAuthToken, error) {
	// The token contains its family ID, so read once to derive the marker key,
	// then use Lua for an atomic revocation-marker plus token read.
	raw, err := r.client.Get(ctx, tokenKey(jti)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var token models.OAuthToken
	if err := json.Unmarshal(raw, &token); err != nil {
		return nil, fmt.Errorf("decode live token: %w", err)
	}
	activeRaw, err := getActiveTokenScript.Run(ctx, r.client, []string{
		tokenKey(jti), revokedFamilyKey(token.FamilyID),
	}).Text()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(activeRaw), &token); err != nil {
		return nil, fmt.Errorf("decode live token: %w", err)
	}
	if !token.ExpiresAt.After(time.Now()) {
		return nil, ErrNotFound
	}
	return &token, nil
}

func (r *RedisTokenStore) RevokeFamily(ctx context.Context, familyID string) (int64, error) {
	return revokeTokenFamilyScript.Run(ctx, r.client, []string{
		tokenFamilyKey(familyID), revokedFamilyKey(familyID),
	}).Int64()
}

func liveToken(token models.OAuthToken) ([]byte, time.Duration, error) {
	if token.JTI == "" || token.FamilyID == "" {
		return nil, 0, errors.New("token jti and family are required")
	}
	ttl := time.Until(token.ExpiresAt)
	if ttl <= 0 {
		return nil, 0, errors.New("token is already expired")
	}
	raw, err := json.Marshal(token)
	return raw, ttl, err
}

func ttlMillis(ttl time.Duration) int64 {
	ms := ttl.Milliseconds()
	if ms < 1 {
		return 1
	}
	return ms
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
