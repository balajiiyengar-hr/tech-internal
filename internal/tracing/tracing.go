package tracing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"tech-internal/internal/apierr"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const exporterEndpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"

func noopShutdown(context.Context) error { return nil }

// Init configures OTLP/HTTP tracing. An explicitly empty endpoint disables
// tracing, which is useful for tests. If unset, the local Jaeger endpoint is used.
// The returned shutdown function is always safe to call, even on error.
func Init(ctx context.Context, serviceName, environment string) (func(context.Context) error, error) {
	endpoint, present := os.LookupEnv(exporterEndpointEnv)
	if !present {
		endpoint = "http://localhost:4319"
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return noopShutdown, nil
	}
	if !strings.HasSuffix(endpoint, "/v1/traces") {
		endpoint = strings.TrimRight(endpoint, "/") + "/v1/traces"
	}

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return noopShutdown, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	// resource.Default already carries the SDK's semconv schema URL, so the
	// service attributes must be schemaless to avoid a conflicting-schema merge.
	attrs := []attribute.KeyValue{semconv.ServiceName(serviceName)}
	if environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentNameKey.String(environment))
	}
	res, err := sdkresource.Merge(sdkresource.Default(), sdkresource.NewSchemaless(attrs...))
	if err != nil {
		return noopShutdown, fmt.Errorf("create trace resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return provider.Shutdown, nil
}

// Middleware creates one server span per request, extracts an incoming W3C
// traceparent, and returns the active traceparent to callers.
func Middleware() gin.HandlerFunc {
	tracer := otel.Tracer("tech-internal/http")

	return func(c *gin.Context) {
		start := time.Now()
		parentCtx := otel.GetTextMapPropagator().Extract(
			c.Request.Context(),
			propagation.HeaderCarrier(c.Request.Header),
		)
		spanName := c.Request.Method + " " + c.Request.URL.Path
		ctx, span := tracer.Start(parentCtx, spanName, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		c.Request = c.Request.WithContext(ctx)

		carrier := propagation.HeaderCarrier(http.Header{})
		otel.GetTextMapPropagator().Inject(ctx, carrier)
		if traceparent := carrier.Get("traceparent"); traceparent != "" {
			c.Header("traceparent", traceparent)
		}

		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		span.SetName(c.Request.Method + " " + route)
		span.SetAttributes(
			attribute.String("http.request.method", c.Request.Method),
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", status),
			attribute.Int64("http.request.duration_ns", time.Since(start).Nanoseconds()),
			attribute.String("url.path", c.Request.URL.Path),
		)
		if code := c.GetString(apierr.ContextKey); code != "" {
			span.SetAttributes(attribute.String("error.code", code))
		}
		if len(c.Errors) > 0 {
			err := errors.New(c.Errors.String())
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
	}
}
