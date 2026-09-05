# New-machine setup

This is the clone-and-run path for a new development machine.

## 1. Install prerequisites

- Git
- Docker Desktop with Docker Compose
- Go **1.25.0**, matching the `go` directive in [`go.mod`](./go.mod)
- [k6](https://grafana.com/docs/k6/latest/set-up/install-k6/) (optional, for load tests)

Confirm the tools are available:

```bash
git --version
docker --version
docker compose version
go version
```

## 2. Clone the repository

Replace `<repository-url>` with the URL supplied by the team:

```bash
git clone <repository-url> tech-internal
cd tech-internal
```

## 3. Start the complete stack

```bash
docker compose up -d --build
```

This builds and starts the UI and API plus PostgreSQL, Redis, Prometheus,
Grafana, and Jaeger. The API runs migrations and seeds the local development
organization on first boot.

| Service | Local address or port |
|---|---|
| UI | `http://localhost:3100` |
| API | `http://localhost:8080` |
| PostgreSQL | `localhost:5434` |
| Redis | `localhost:6380` |
| Prometheus | `http://localhost:9090` |
| Grafana | `http://localhost:3200` |
| Jaeger UI | `http://localhost:16687` |
| Jaeger OTLP gRPC | `localhost:4320` |
| Jaeger OTLP HTTP | `http://localhost:4319` |

Check container status and logs if a service is not ready:

```bash
docker compose ps
docker compose logs api ui
```

## 4. Host fallback for API/UI build failures

On corporate networks, Docker image builds may fail while downloading Go
modules from `proxy.golang.org`, with an error such as
`tls: failed to verify certificate`. This usually means the Docker build
container does not trust the corporate TLS inspection CA even though the host
does. Keep TLS verification enabled. Install the corporate root CA for Docker
Desktop/build containers with help from your IT team, or use this host
fallback:

```bash
docker compose up -d postgres redis prometheus grafana jaeger

OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4319 ./scripts/run-api.sh
./scripts/run-ui.sh
```

The host must have the matching Go version and access to download the modules.
The API exports traces to the host-published Jaeger collector.

Use either the compose API/UI or the host scripts, never both at once. They
would contend for ports `8080` and `3100`. To switch from the complete compose
stack to host processes:

```bash
docker compose stop api ui
```

## 5. Sign in and inspect the stack

Seeded organization administrator:

- Organization: `techhr.com`
- Email: `admin@techhr.com`
- Password: `Admin@123`

Useful URLs:

- Member login: [http://localhost:3100/](http://localhost:3100/)
- Organization admin login: [http://localhost:3100/admin/login](http://localhost:3100/admin/login)
- API: [http://localhost:8080](http://localhost:8080)
- API metrics: [http://localhost:8080/metrics](http://localhost:8080/metrics)
- Grafana API dashboard: [http://localhost:3200/d/tech-internal-api/tech-internal-api-observability](http://localhost:3200/d/tech-internal-api/tech-internal-api-observability)
- Jaeger: [http://localhost:16687](http://localhost:16687)
- Prometheus: [http://localhost:9090](http://localhost:9090)

Prometheus scrapes the compose API at `api:8080/metrics`. For tracing details,
see [`docs/tracing.md`](./docs/tracing.md).

## 6. Configuration, API rules, and verification

All supported configuration is documented in
[`env-variables.md`](./env-variables.md). Backend contributors must follow
[`api-rules.md`](./api-rules.md), including its data-access and performance
requirements.

### Before every commit

1. Run `./scripts/test-coverage.sh`, exactly as the pre-commit hook does.
2. The hook runs unit `go test ./...`, enforces the 90% gated Go coverage
   floor, and fails on lower coverage, fewer passing tests, or a package that
   passed in `reports/coverage/latest.txt` but now fails.
3. Install the local repository hook once after cloning (bypasses Uber
   `asd-cli` / `ussh` on commit for this repo):

   ```bash
   ./scripts/setup-githooks.sh
   ```

   Equivalent: `git config core.hooksPath scripts/githooks`.

If no report exists, the first green run creates it. Every later green run
updates the shared coverage, package, and test-count baseline. k6 is not part
of the pre-commit hook.

Run the tests and the mixed 200 requests/second load test:

```bash
./scripts/test-coverage.sh
RATE=200 DURATION=20s ./scripts/loadtest/run-mixed.sh
```

k6 is required only for the load test. See
[`docs/load-testing.md`](./docs/load-testing.md) for details.

Logout revokes the access/refresh token family. OAuth grants, refresh,
revocation, and logout behavior are documented in [`OAuth.md`](./OAuth.md),
with the machine-readable contract in [`OAuth.yaml`](./OAuth.yaml).

## Stop the stack

```bash
docker compose down
```

Add `-v` only when you intentionally want to delete the local PostgreSQL data:

```bash
docker compose down -v
```
