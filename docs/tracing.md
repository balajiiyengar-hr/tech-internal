# Distributed tracing

The API exports OpenTelemetry spans over OTLP/HTTP to Jaeger. Each request has
a parent server span, with child spans for PostgreSQL and Redis operations.
Incoming W3C `traceparent` headers are continued, and the active `traceparent`
is returned in the response.

## Run locally

The full stack enables tracing automatically:

```bash
docker compose up -d --build
```

Open the Jaeger UI at [http://127.0.0.1:16686](http://127.0.0.1:16686).
Select `tech-internal-api`, choose an operation (for example,
`GET /api/v2/organizations/:id/members`), and click **Find Traces**. Sort by
duration or raise **Min Duration** to isolate slow requests, then expand a
trace to compare its HTTP, PostgreSQL, and Redis spans.

For a host-run API, `OTEL_EXPORTER_OTLP_ENDPOINT` defaults to
`http://localhost:4319`. Set it to another OTLP/HTTP collector URL as needed:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4319
go run ./cmd/server
```

Set the variable to an explicitly empty value to disable tracing, including in
tests:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT= go test ./...
```

The `trace.id` and `span.id` fmtlog fields identify the same request span shown
in Jaeger. `X-Request-ID` remains a separate request correlation identifier.
