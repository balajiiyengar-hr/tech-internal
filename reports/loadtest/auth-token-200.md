# Auth-token-only performance test — 200 TPS

**Result: PASS.** All three expected flows achieved 100% success, with zero
5xx responses and zero dropped iterations against the 99% target.

## Profile

- Warmup: 10 seconds.
- Measured steady state: 3 minutes, 36,002 iterations.
- Total target: 200 iterations/s: `authtoken` 140/s (70%),
  `refreshtoken` 40/s (20%), logout 20/s (10%).
- Password login was used only to pre-seed independent session families before
  measurement. It was not part of measured steady-state traffic.

## k6 results

| Flow | Requests | Effective RPS | Success | Status | p50 | p95 | p99 | Max |
|---|---:|---:|---:|---|---:|---:|---:|---:|
| Access validation / `GET /me` | 25,201 | 140.01 | 100% | 25,201 × 200 | 1.77 ms | 3.44 ms | 6.76 ms | 52.56 ms |
| Refresh rotation | 7,201 | 40.01 | 100% | 7,201 × 200 | 2.52 ms | 4.37 ms | 7.18 ms | 49.72 ms |
| Logout / family revocation | 3,600 | 20.00 | 100% | 3,600 × 204 | 1.19 ms | 2.17 ms | 3.17 ms | 16.40 ms |

Overall measured traffic had 0 dropped iterations and 0 5xx responses. k6's
39,945 total HTTP requests include 3,943 session-seeding/setup requests and are
not the steady-state throughput denominator.

## Prometheus / Grafana

Prometheus route histograms over the measured window reported:

| Route | p50 | p95 | p99 | Status/error group |
|---|---:|---:|---:|---|
| `GET /api/v1/me` | 1.80 ms | 4.14 ms | 7.41 ms | 2xx / none |
| `POST /api/v1/oauth/token/refresh` | 2.26 ms | 4.81 ms | 8.61 ms | 2xx / none |
| `POST /api/v1/auth/logout` | 0.91 ms | 2.36 ms | 3.55 ms | 2xx / none |

Redis performed about 320 `get` commands/s, 240 bounded `other` commands/s
(primarily Lua `evalsha`), and 40 pipelines/s. Mean command times were
0.61 ms, 0.55 ms, and 0.50 ms respectively. Prometheus p95/p99 values cluster
at 4.75–4.97 ms because the Redis histogram's first bucket is 5 ms; Jaeger
provides the more useful sub-5-ms distribution. Pool hits were ~108,010 with
0 misses and 0 timeouts; total connections peaked at 31 and idle connections
never fell below 25.

There were **zero PostgreSQL queries or pool acquires** during steady state.
The pool remained 10 idle / 0 in-use / 50 max, so pool-acquire latency is not
applicable to the token hot path.

| Process | CPU avg / p95 / max | RSS avg / max |
|---|---|---|
| API | 11.23% / 15.80% / 16.90% | 56.46 / 56.52 MiB |
| Redis | 5.14% / 6.24% / 13.33% | 113.57 / 114.10 MiB |
| PostgreSQL | 0.17% / 1.14% / 1.61% | 414.91 / 415.10 MiB |
| Jaeger | 0.55% / 1.02% / 1.57% | 327.82 / 544.90 MiB |

The API was the busiest CPU process but had ample headroom. Jaeger was not
OOM-killed and did not restart, but full sampling grew its in-memory store to
544.9 MiB; ratio sampling should be added before higher-rate or longer traced
runs.

Grafana: [Tech Internal API Observability](http://localhost:3200/d/tech-internal-api/tech-internal-api-observability?orgId=1&from=1788686042058&to=1788686222047)

## Jaeger

Two hundred traces per flow were sampled from the measured window.

| Flow | Sample median | Sample p95 | Median trace children | Slow p95 trace children |
|---|---:|---:|---|---|
| Access validation | 1.37 ms | 2.60 ms | Redis `get` 0.565 ms, `evalsha` 0.362 ms, cache `get` 0.299 ms | 1.038 ms, 0.685 ms, 0.797 ms |
| Refresh rotation | 2.08 ms | 4.14 ms | `get` 0.554 ms, `evalsha` 0.513 + 0.426 ms, cache pipeline 0.391 ms | rotation `evalsha` 1.962 ms, pipeline 0.659 ms, validation spans 0.595 + 0.496 ms |
| Logout | 0.77 ms | 1.55 ms | revocation `evalsha` 0.687 ms | revocation `evalsha` 1.468 ms |

JWT verification, signing, middleware, JSON, and handler work are not separate
spans in the current instrumentation; they occupy the small root-span residual.
No PostgreSQL query, prepare, or pool-acquire span appeared in any of the 600
inspected auth traces.

Jaeger UI: [tech-internal-api traces](http://localhost:16687/search?service=tech-internal-api)

## Correctness and cleanup

- Old refresh token after rotation: 401.
- New access token before logout: 200.
- Logout: 204.
- Access token after logout: 401.
- Refresh token after logout: 401.
- PostgreSQL `oauth_tokens`: 0.
- Generated test users: 0.
- Active generated test token keys: 0.
- Generated recent revocation tombstones: 0 after cleanup.
- `techhr.com` and `admin@techhr.com` remain present.

## Raw artifacts

- `reports/loadtest/auth-token-200.summary.json`
- `reports/loadtest/auth-token-200.raw.json`
- `reports/loadtest/auth-token-200.txt`
- `reports/loadtest/auth-token-200-infra.csv`
- `reports/loadtest/auth-token-200-infra.summary.json`
- `reports/loadtest/auth-token-200-prometheus.json`
- `reports/loadtest/auth-token-200-jaeger.json`
- `reports/loadtest/auth-token-200-jaeger-summary.json`
- `reports/loadtest/auth-token-200-correctness.json`
