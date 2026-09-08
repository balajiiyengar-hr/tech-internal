-- RedisTokenStore (internal/repository/tokens_redis.go) has been the sole
-- OAuthTokenStore since 5788895 ("Store OAuth tokens in Redis..."); the
-- Postgres oauth_tokens table (003-005) is no longer read or written by the
-- running service.
DROP TABLE IF EXISTS oauth_tokens;
