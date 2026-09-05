package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appmetrics "tech-internal/internal/metrics"
	"tech-internal/internal/models"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const defaultAuthCacheEntries = 5000

type cachedIdentity struct {
	User        models.User `json:"user"`
	HasPassword bool        `json:"has_password"`
}

// AuthCache stores login identity metadata and protected password hashes in
// separate Redis keys. Neither key has a TTL; the ZSET keeps the working set
// bounded and records recency for Redis-native LRU eviction.
type AuthCache struct {
	client     *redis.Client
	maxEntries int64
}

func NewAuthCache(client *redis.Client, maxEntries int) *AuthCache {
	if maxEntries <= 0 {
		maxEntries = defaultAuthCacheEntries
	}
	return &AuthCache{client: client, maxEntries: int64(maxEntries)}
}

func cacheMember(userType, identifier string) string {
	normalized := strings.ToLower(strings.TrimSpace(identifier))
	return strings.ToLower(strings.TrimSpace(userType)) + ":" +
		base64.RawURLEncoding.EncodeToString([]byte(normalized))
}

func identityCacheKey(member string) string { return "auth:identity:" + member }
func passwordCacheKey(member string) string { return "auth:pwd:" + member }
func personCacheKey(personID string) string { return "auth:person:" + personID + ":identities" }

func (c *AuthCache) Get(ctx context.Context, domain, userType, identifier string) (*models.User, bool) {
	member := cacheMember(userType, identifier)
	pipe := c.client.Pipeline()
	identityCmd := pipe.Get(ctx, identityCacheKey(member))
	passwordCmd := pipe.Get(ctx, passwordCacheKey(member))
	pipe.ZAdd(ctx, "auth:lru", redis.Z{Score: float64(redisTime()), Member: member})
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		appmetrics.ObserveCacheRequest("identity", "miss")
		return nil, false
	}

	raw, err := identityCmd.Bytes()
	if err != nil {
		appmetrics.ObserveCacheRequest("identity", "miss")
		return nil, false
	}
	var entry cachedIdentity
	if json.Unmarshal(raw, &entry) != nil ||
		!strings.EqualFold(entry.User.Domain, strings.TrimSpace(domain)) {
		appmetrics.ObserveCacheRequest("identity", "miss")
		return nil, false
	}
	appmetrics.ObserveCacheRequest("identity", "hit")
	if entry.HasPassword {
		hash, err := passwordCmd.Result()
		if err != nil {
			appmetrics.ObserveCacheRequest("password", "miss")
			return nil, false
		}
		appmetrics.ObserveCacheRequest("password", "hit")
		entry.User.PasswordHash = &hash
	}
	return &entry.User, true
}

var putAuthCacheScript = redis.NewScript(`
redis.call('SET', KEYS[1], ARGV[1])
if ARGV[2] == '1' then
  redis.call('SET', KEYS[2], ARGV[3])
else
  redis.call('DEL', KEYS[2])
end
redis.call('ZADD', KEYS[3], ARGV[4], ARGV[5])
redis.call('SADD', KEYS[4], ARGV[5])
local excess = redis.call('ZCARD', KEYS[3]) - tonumber(ARGV[6])
if excess > 0 then
  local victims = redis.call('ZRANGE', KEYS[3], 0, excess - 1)
  for _, victim in ipairs(victims) do
    local metadata = redis.call('GET', 'auth:identity:' .. victim)
    if metadata then
      local ok, decoded = pcall(cjson.decode, metadata)
      if ok and decoded.user and decoded.user.person_id then
        redis.call('SREM', 'auth:person:' .. decoded.user.person_id .. ':identities', victim)
      end
    end
    redis.call('DEL', 'auth:identity:' .. victim, 'auth:pwd:' .. victim)
    redis.call('ZREM', KEYS[3], victim)
  end
end
return 1
`)

func (c *AuthCache) Put(ctx context.Context, user *models.User) error {
	if user == nil {
		return nil
	}
	member := cacheMember(user.Type, user.Identifier)
	copyOfUser := *user
	hash := ""
	hasPassword := copyOfUser.PasswordHash != nil
	if hasPassword {
		hash = *copyOfUser.PasswordHash
	}
	copyOfUser.PasswordHash = nil
	raw, err := json.Marshal(cachedIdentity{User: copyOfUser, HasPassword: hasPassword})
	if err != nil {
		return fmt.Errorf("marshal auth cache: %w", err)
	}
	hasPasswordArg := "0"
	if hasPassword {
		hasPasswordArg = "1"
	}
	return putAuthCacheScript.Run(ctx, c.client,
		[]string{identityCacheKey(member), passwordCacheKey(member), "auth:lru", personCacheKey(user.PersonID)},
		raw, hasPasswordArg, hash, redisTime(), member, c.maxEntries,
	).Err()
}

var invalidatePersonScript = redis.NewScript(`
local members = redis.call('SMEMBERS', KEYS[1])
for _, member in ipairs(members) do
  redis.call('DEL', 'auth:identity:' .. member, 'auth:pwd:' .. member)
  redis.call('ZREM', KEYS[2], member)
end
redis.call('DEL', KEYS[1])
return #members
`)

func (c *AuthCache) InvalidatePerson(ctx context.Context, personID string) error {
	if strings.TrimSpace(personID) == "" {
		return nil
	}
	return invalidatePersonScript.Run(ctx, c.client,
		[]string{personCacheKey(personID), "auth:lru"}).Err()
}

// redisTime is isolated so cache recency can be deterministic in tests.
var redisTime = func() int64 {
	return time.Now().UnixMilli()
}

type DBLimiter struct{ slots chan struct{} }

func NewDBLimiter(max int) *DBLimiter {
	if max <= 0 {
		max = 40
	}
	return &DBLimiter{slots: make(chan struct{}, max)}
}

func (l *DBLimiter) run(ctx context.Context, fn func() error) error {
	select {
	case l.slots <- struct{}{}:
		defer func() { <-l.slots }()
		return fn()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CachedUserRepository is Redis-first and bounds only Postgres fallback/write
// concurrency, leaving cache hits independent of the database pool.
type CachedUserRepository struct {
	base    *UserRepository
	cache   *AuthCache
	limiter *DBLimiter
	loads   singleflight.Group
}

func NewCachedUserRepository(base *UserRepository, cache *AuthCache, limiter *DBLimiter) *CachedUserRepository {
	if limiter == nil {
		limiter = NewDBLimiter(40)
	}
	return &CachedUserRepository{base: base, cache: cache, limiter: limiter}
}

func (r *CachedUserRepository) Get(ctx context.Context, domain, userType, identifier string) (*models.User, error) {
	if user, ok := r.cache.Get(ctx, domain, userType, identifier); ok {
		return user, nil
	}
	key := strings.ToLower(strings.TrimSpace(domain)) + ":" + cacheMember(userType, identifier)
	value, err, _ := r.loads.Do(key, func() (any, error) {
		if user, ok := r.cache.Get(ctx, domain, userType, identifier); ok {
			return user, nil
		}
		var user *models.User
		err := r.limiter.run(ctx, func() error {
			var loadErr error
			user, loadErr = r.base.Get(ctx, domain, userType, identifier)
			return loadErr
		})
		if err != nil {
			return nil, err
		}
		if err := r.cache.Put(ctx, user); err != nil {
			return nil, err
		}
		return user, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(*models.User), nil
}

func (r *CachedUserRepository) ListByDomain(ctx context.Context, domain string, limit, offset int) ([]models.User, int64, error) {
	var users []models.User
	var total int64
	err := r.limiter.run(ctx, func() error {
		var err error
		users, total, err = r.base.ListByDomain(ctx, domain, limit, offset)
		return err
	})
	return users, total, err
}

func (r *CachedUserRepository) Create(ctx context.Context, user models.User, passwordHash *string) error {
	if err := r.limiter.run(ctx, func() error { return r.base.Create(ctx, user, passwordHash) }); err != nil {
		return err
	}
	return r.loadAndPut(ctx, user.Domain, user.Type, user.Identifier)
}

func (r *CachedUserRepository) UpdatePasswordHash(ctx context.Context, domain, userType, identifier, passwordHash string) error {
	if err := r.limiter.run(ctx, func() error {
		return r.base.UpdatePasswordHash(ctx, domain, userType, identifier, passwordHash)
	}); err != nil {
		return err
	}
	return r.loadAndPut(ctx, domain, userType, identifier)
}

func (r *CachedUserRepository) loadAndPut(ctx context.Context, domain, userType, identifier string) error {
	var user *models.User
	if err := r.limiter.run(ctx, func() error {
		var err error
		user, err = r.base.Get(ctx, domain, userType, identifier)
		return err
	}); err != nil {
		return err
	}
	return r.cache.Put(ctx, user)
}

func (r *CachedUserRepository) loadByOrganizationAndPut(ctx context.Context, organizationID, userType, identifier string) error {
	var user *models.User
	if err := r.limiter.run(ctx, func() error {
		var err error
		user, err = r.base.GetByOrganizationID(ctx, organizationID, userType, identifier)
		return err
	}); err != nil {
		return err
	}
	return r.cache.Put(ctx, user)
}

func (r *CachedUserRepository) invalidatePerson(ctx context.Context, personID string) error {
	return r.cache.InvalidatePerson(ctx, personID)
}

// CachedDomainRepository removes the remaining domain-settings lookup from
// login/OTP cache hits. Admin updates are DB-first, then write-through.
type CachedDomainRepository struct {
	base    *DomainRepository
	client  *redis.Client
	limiter *DBLimiter
}

func NewCachedDomainRepository(base *DomainRepository, client *redis.Client, limiter *DBLimiter) *CachedDomainRepository {
	if limiter == nil {
		limiter = NewDBLimiter(40)
	}
	return &CachedDomainRepository{base: base, client: client, limiter: limiter}
}

func domainCacheKey(domain string) string {
	return "auth:domain:" + strings.ToLower(strings.TrimSpace(domain))
}

func (r *CachedDomainRepository) Get(ctx context.Context, domain string) (*models.DomainSettings, error) {
	if raw, err := r.client.Get(ctx, domainCacheKey(domain)).Bytes(); err == nil {
		var settings models.DomainSettings
		if json.Unmarshal(raw, &settings) == nil {
			appmetrics.ObserveCacheRequest("domain", "hit")
			return &settings, nil
		}
	}
	appmetrics.ObserveCacheRequest("domain", "miss")
	var settings *models.DomainSettings
	if err := r.limiter.run(ctx, func() error {
		var err error
		settings, err = r.base.Get(ctx, strings.ToLower(strings.TrimSpace(domain)))
		return err
	}); err != nil {
		return nil, err
	}
	if err := r.put(ctx, settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func (r *CachedDomainRepository) Update(ctx context.Context, settings models.DomainSettings) error {
	if err := r.limiter.run(ctx, func() error { return r.base.Update(ctx, settings) }); err != nil {
		return err
	}
	return r.put(ctx, &settings)
}

func (r *CachedDomainRepository) put(ctx context.Context, settings *models.DomainSettings) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, domainCacheKey(settings.Domain), raw, 0).Err()
}
