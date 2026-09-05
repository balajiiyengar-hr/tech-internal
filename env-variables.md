# Environment variables

All process configuration for Tech Internal Portal. Change a value with `export NAME=value` before `go run`, or set it in the container/orchestrator.

Local ports used by this project (Grafana in compose is **3200**, not 3000; UI is **3100**, not 3000):

| Service | Host port |
|---------|-----------|
| UI (`cmd/web`) | **3100** (`UI_PORT`) |
| API (`cmd/server`) | **8080** (`PORT`) |
| Postgres (docker compose) | **5434** |
| Redis (docker compose) | **6380** |
| Prometheus (docker compose) | **9090** |
| Grafana (docker compose) | **3200** (container 3000) |
| Jaeger UI (docker compose) | **16686** |
| Jaeger OTLP HTTP / gRPC | **4318** / **4317** |

Local one-command path: `docker compose up -d --build`. This starts all seven services. CORS for the UI includes both localhost forms on **3100**. Metrics: `GET http://127.0.0.1:8080/metrics`. Grafana: [http://127.0.0.1:3200/d/tech-internal-api/tech-internal-api-observability](http://127.0.0.1:3200/d/tech-internal-api/tech-internal-api-observability). Jaeger: [http://127.0.0.1:16686](http://127.0.0.1:16686).

The compose API uses `postgres:5432` and `redis:6379` on the internal network; the browser-facing UI config still uses `http://localhost:8080/api/v1`. `scripts/run-api.sh` and `scripts/run-ui.sh` are optional host-process alternatives. Do not run them while the compose API/UI are running because ports **8080** and **3100** will conflict.

---

## API (`cmd/server`)

Required in production: `JWT_SECRET`, `DATA_ENCRYPTION_KEY`, `DATABASE_URL`, `REDIS_URL`, `CORS_ORIGINS`.

| Variable | Default | Required in prod | Description |
|----------|---------|------------------|-------------|
| `PORT` | `8080` | No | HTTP listen port for the REST API |
| `CORS_ORIGINS` | `*` | **Yes** (lock down) | Comma-separated UI origins allowed to call the API. Example: `https://portal.techhr.com,http://127.0.0.1:3100`. Use `*` only for local experiments |
| `DATABASE_URL` | `postgres://portal:portal@localhost:5434/tech_internal?sslmode=disable` | **Yes** | Postgres DSN (`pgx`). Host, user, password, db name, SSL mode |
| `REDIS_URL` | `redis://localhost:6380/0` | **Yes** | Redis DSN for OTPs and the bounded auth identity/password cache |
| `JWT_SECRET` | `tech-internal-dev-secret-change-in-production` | **Yes** | HMAC secret for access tokens. Changing it invalidates all sessions |
| `ACCESS_TOKEN_HOURS` | unset | No | Access-token lifetime. Falls back to `JWT_EXPIRY_HOURS` |
| `JWT_EXPIRY_HOURS` | `24` | No | Compatibility fallback for access-token lifetime |
| `REFRESH_TOKEN_HOURS` | `96` | No | Refresh-token lifetime |
| `PASSWORD_KDF` | `argon2id` | No | New password hashes: `argon2id` (config / production default) or salted `sha256`. `./scripts/run-api.sh` exports `PASSWORD_KDF=sha256` unless you override it. Existing sealed hashes of either type continue to verify and are **not** rewritten on login or API restart. The seeded admin is sealed once (`enc:v1:...`); changing KDF later does not rehash it. Never use MD5 |
| `DATA_ENCRYPTION_KEY` | `tech-internal-dev-data-key-change-me` | **Yes** | AES-256-GCM key for sealed password hashes and OTPs. 32-byte hex, 32-byte base64, or any string (SHA-256 derived). Changing it invalidates stored passwords and in-flight OTPs |
| `OTP_TTL_SECONDS` | ignored | No | Compatibility only. SMS OTP TTL is fixed at 120 seconds in request and verify flows |
| `DEV_LOG_OTP` | `false` | No | If `true`, fmtlog includes `otp.dev_code` and `POST /auth/otp/request` returns `dev_code`, which the login page shows and prefills (local SMS simulation only; never enable in production) |
| `DB_MAX_CONNS` | `50` | No | Postgres pool maximum connections |
| `DB_MIN_CONNS` | `10` | No | Postgres pool minimum (warm) connections |
| `DB_MAX_CONN_LIFETIME_MIN` | `30` | No | Recycle pool connections after this many minutes |
| `MAX_INFLIGHT_DB` | `40` | No | Shared cap for auth cache fallbacks and writes; keep below `DB_MAX_CONNS` |
| `REDIS_POOL_SIZE` | `100` | No | Redis client connection pool maximum |
| `REDIS_MIN_IDLE_CONNS` | `20` | No | Warm idle Redis connections |
| `AUTH_CACHE_SIZE` | `5000` | No | Redis LRU cap for identity/password working-set entries |
| `MIGRATION_DIR` | `./migrations` | No | Directory of `*.sql` files applied at API startup |
| `SERVICE_NAME` | `tech-internal-api` | No | `service.name` in fmtlog / Kibana |
| `APP_ENV` | `dev` | No | `service.environment` in fmtlog (`dev`, `staging`, `prod`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://localhost:4319` | No | OTLP/HTTP collector URL for a host-run API. Compose uses the container-internal `http://jaeger:4318`; this repo publishes it on host port `4319` to avoid local collector conflicts. Set an explicitly empty value to disable tracing |

Example production snippet:

```bash
export PORT=8080
export APP_ENV=prod
export SERVICE_NAME=tech-internal-api
export CORS_ORIGINS=https://portal.techhr.com
export DATABASE_URL='postgres://portal:SECRET@postgres.internal:5432/tech_internal?sslmode=require'
export REDIS_URL='redis://redis.internal:6379/0'
export JWT_SECRET='replace-with-long-random-secret'
export DATA_ENCRYPTION_KEY='replace-with-32-byte-hex-or-long-passphrase'
export ACCESS_TOKEN_HOURS=24
export JWT_EXPIRY_HOURS=24
export REFRESH_TOKEN_HOURS=96
export PASSWORD_KDF=argon2id
export OTP_TTL_SECONDS=120
export DEV_LOG_OTP=false
export DB_MAX_CONNS=50
export DB_MIN_CONNS=10
export DB_MAX_CONN_LIFETIME_MIN=30
export MAX_INFLIGHT_DB=40
export REDIS_POOL_SIZE=100
export REDIS_MIN_IDLE_CONNS=20
export AUTH_CACHE_SIZE=5000
export MIGRATION_DIR=./migrations
export OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.internal:4318
```

---

## UI (`cmd/web`)

The browser never talks to Postgres or Redis. It only uses `API_BASE_URL`.
No V2 auth/org feature flag is required: V2 is enabled by migration 004.

| Variable | Default | Required in prod | Description |
|----------|---------|------------------|-------------|
| `UI_PORT` | `3100` | No | HTTP listen port for static UI. Default is **3100** so it does not collide with Grafana on 3000 |
| `UI_DIR` | `./frontend` | No | Directory of HTML/CSS/JS |
| `API_BASE_URL` | `http://127.0.0.1:8080/api/v1` | **Yes** | V1 REST prefix (no trailing slash). The UI derives `/api/v2` from this value for member/role APIs |
| `SERVICE_NAME` | `tech-internal-ui` | No | `service.name` in fmtlog |
| `APP_ENV` | `dev` | No | `service.environment` in fmtlog |

Example production snippet:

```bash
export UI_PORT=3100
export UI_DIR=./frontend
export API_BASE_URL=https://api.techhr.com/api/v1
export SERVICE_NAME=tech-internal-ui
export APP_ENV=prod
```

Local split:

```bash
# Terminal 1 — API (allow the UI origin on 3100)
export CORS_ORIGINS=http://127.0.0.1:3100,http://localhost:3100
go run ./cmd/server

# Terminal 2 — UI
export UI_PORT=3100
export API_BASE_URL=http://127.0.0.1:8080/api/v1
go run ./cmd/web
```

Open **http://127.0.0.1:3100** for member login or
**http://127.0.0.1:3100/admin/login** for org-admin login.

---

## Docker Compose (full local stack)

`docker-compose.yml` builds the API and UI and starts them with Postgres, Redis, Prometheus, Grafana, and Jaeger.

| Variable | Value in compose | Description |
|----------|------------------|-------------|
| `POSTGRES_USER` | `portal` | Database user (must match `DATABASE_URL`) |
| `POSTGRES_PASSWORD` | `portal` | Database password (local only) |
| `POSTGRES_DB` | `tech_internal` | Database name |

Published ports: API `8080:8080`, UI `3100:3100`, Postgres `5434:5432`, Redis `6380:6379`, Prometheus `9090:9090`, Grafana `3200:3000`, Jaeger UI `16686:16686`, and Jaeger OTLP `4317:4317` / `4318:4318`. Prometheus scrapes `api:8080/metrics` over the compose network every 1s (`deploy/prometheus/prometheus.yml`); new shared stores must publish low-cardinality client latency and pool-utilization metrics there.

---

## Docker images

**API** (`Dockerfile`): `PORT` (expose 8080), `MIGRATION_DIR=./migrations`, plus all API variables above.

**UI** (`Dockerfile.web`): `UI_PORT=3100`, `UI_DIR=./frontend`, plus `API_BASE_URL` at runtime.

---

## Not configurable via env (code defaults)

| Setting | Value | Where |
|---------|--------|--------|
| Postgres max idle time | 5 minutes | same |
| Postgres health check | 30 seconds | same |
| OTP Redis key | `otp:{domain}:{mobile}` | `internal/otp/store.go` |
| SMS OTP TTL | 120 seconds | `internal/otp/store.go` |

---

## Secrets checklist

Do not commit real values. Rotate if leaked.

1. `JWT_SECRET` — session forging
2. `DATA_ENCRYPTION_KEY` — password/OTP ciphertext at rest
3. `DATABASE_URL` password
4. Redis AUTH if you add it to `REDIS_URL`

Related: [api-rules.md](./api-rules.md), [api-docs.md](./api-docs.md), [apis.md](./apis.md), [OAuth.md](./OAuth.md), [docs/portalauth.md](./docs/portalauth.md), [docs/load-testing.md](./docs/load-testing.md), [README.md](./README.md).
