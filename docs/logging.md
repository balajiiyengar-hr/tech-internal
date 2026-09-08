# Logging — fmtlog to a warehouse

Both `cmd/server` and `cmd/web` write **one ECS JSON object per line (NDJSON) to stdout**. That is the only supported log sink in-process.

Do **not** write logs from Gin handlers into Postgres or Kafka. At auth TPS that adds tail latency and couples login availability to the warehouse.

## Event shape

```json
{
  "@timestamp": "2026-09-04T20:55:01.123Z",
  "ecs.version": "8.11.0",
  "log.level": "info",
  "message": "http_request",
  "service.name": "tech-internal-api",
  "service.environment": "prod",
  "event.dataset": "tech-internal-api.http_request",
  "event.action": "http_request",
  "trace.id": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6",
  "http.request.id": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6",
  "http.request.method": "GET",
  "url.path": "/api/v1/apps",
  "http.response.status_code": 200,
  "event.duration": 2400000,
  "client.ip": "10.0.1.12",
  "user.domain": "techhr.com",
  "user.id": "admin@techhr.com",
  "user.roles": "admin",
  "error.code": "AUTH_INVALID_CREDENTIALS"
}
```

| Field | Meaning |
|-------|---------|
| `@timestamp` | RFC3339Nano UTC |
| `log.level` | `info` / `warn` (4xx) / `error` (5xx) / `fatal` |
| `message` | Event name (`http_request`, `server_started`, `otp_issued`, …) |
| `service.name` | `SERVICE_NAME` |
| `service.environment` | `APP_ENV` |
| `trace.id` | `X-Request-ID` in or generated |
| `event.duration` | Nanoseconds (ECS) |
| `error.code` | Bounded `custom_error_code` enum from `internal/apierr` (dropped when empty, i.e. 2xx/3xx); same value tags the `tech_internal_api_http_requests_total`/`..._request_duration_seconds` metrics and the request's trace span, so one value correlates logs, metrics, and traces for a failure |

`GET /api/v1/health` and CORS `OPTIONS` are not access-logged. `GET /metrics` **is** access-logged; Prometheus scrapes it every 1s in local compose, so filter `url.path=/metrics` in the collector if that noise matters. Passwords are never logged. Plaintext OTP appears only as `otp.dev_code` when `DEV_LOG_OTP=true` — drop that field in the collector. `/metrics` request series themselves exclude scrapes (`internal/metrics`).

Stable `message` values are the Kibana/OpenSearch filters. Keep them.

## Recommended pipeline

```
stdout (fmtlog NDJSON)
    → Vector or Fluent Bit (parse JSON, redact, drop health)
    → Kafka topic logs.fmtlog
    → OpenSearch / Elasticsearch (search)
    → optional ClickHouse (long analytics)
    → optional Postgres (audit subset only)
```

**Kafka** is the buffer (backpressure, replay). **OpenSearch** is the search store (ECS field names already match). **Postgres is not a log warehouse** — use it only for a small audit table (`otp_issued`, admin mutations, login failures).

### Collector rules

1. Parse the **whole line** as JSON. If the runtime wraps container logs, decode the inner JSON; do not double-wrap `message`.
2. Keep ECS dotted keys (`http.response.status_code`, `event.duration`).
3. Enrich host/region/pod in the collector, not in Go.
4. Redact `otp.dev_code`, Authorization, cookies.
5. Batch and compress to Kafka (lz4/zstd).

### Kafka

- Topic: `logs.fmtlog` (or `logs.{env}.fmtlog`)
- Key: `service.name` or `trace.id` if you need per-service order; otherwise null
- Retention in Kafka: 3–7 days; warehouse holds the long tail
- Producers: collectors only (`acks=1` or idempotent). The API must not wait on Kafka.

### OpenSearch

Index template for ECS. ILM/ISM example: hot 7 days → UltraWarm 30 → delete 90.

Rough **us-east-1 list** cost (on-demand, 2026 public AWS examples; confirm in Pricing Calculator):

| Logged TPS | Raw / day (≈800 B/line) | Indexed+1 replica / day | Fit | Ballpark / month |
|------------|-------------------------|-------------------------|-----|------------------|
| 10 (dev) | ~0.7 GB | ~2 GB | 1× t3.small | **~$15–30** |
| 100 | ~7 GB | ~20 GB | 2–3 small data nodes | **~$250–500** |
| 200 | ~14 GB | ~40 GB | 3 AZ r6g.large + masters | **~$700–1,200** |
| 1000 | ~69 GB | ~200 GB | 3× r6g.xlarge + UltraWarm | **~$1,500–2,000** |

Assumptions: 1.45× index overhead (AWS example), 1 replica, 30-day hot or 7 hot + UltraWarm. Serverless Classic production floor is about **2 OCU × $0.24 × 730h ≈ $350/month** even idle. NextGen can scale compute to zero when idle. Kafka/MSK, NAT, and data transfer are extra.

Line size grows with `user_agent.original`; trim it in the shipper if cost matters.

## Filebeat (containers)

If Docker nests the JSON in `message`:

```yaml
filebeat.inputs:
  - type: container
    paths: ["/var/lib/docker/containers/*/*.log"]
processors:
  - decode_json_fields:
      fields: ["message"]
      target: ""
```

Raw stdout from systemd/k8s: decode the entire line as JSON (no nested `message` string).

## Postgres audit (optional)

If security needs durable login/admin events, consume Kafka and insert a **narrow** table, e.g. `audit_events(ts, service, action, domain, user_id, payload jsonb)`, partition by day, index `(domain, ts)`. Batch `COPY`. Do not store every `http_request`.
