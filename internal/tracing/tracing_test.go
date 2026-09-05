package tracing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestInitSkipsTracingWhenEndpointIsEmpty(t *testing.T) {
	t.Setenv(exporterEndpointEnv, "")
	before := otel.GetTracerProvider()

	shutdown, err := Init(context.Background(), "tech-internal-api", "test")
	if err != nil {
		t.Fatalf("Init() error = %v, want nil", err)
	}
	if otel.GetTracerProvider() != before {
		t.Fatal("Init() replaced the tracer provider for an empty endpoint")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown() error = %v, want nil", err)
	}
}

// Guards against the resource.Merge schema conflict that made startup fail:
// resource.Default carries the SDK semconv schema URL, so service attributes
// must not declare a second, different one.
func TestInitBuildsResourceWithoutSchemaConflict(t *testing.T) {
	t.Setenv(exporterEndpointEnv, "http://localhost:4318")

	shutdown, err := Init(context.Background(), "tech-internal-api", "test")
	if err != nil {
		t.Fatalf("Init() error = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := shutdown(context.Background()); err != nil {
			t.Errorf("shutdown() error = %v, want nil", err)
		}
	})

	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Fatal("Init() did not install an SDK tracer provider")
	}
}

func TestMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer provider.Shutdown(context.Background())
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	for _, tc := range []struct {
		name string
		code int
		fail bool
		path string
	}{
		{"ok", 204, false, "/ok"},
		{"server error", 500, false, "/error"},
		{"context error", 400, true, "/failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(Middleware())
			r.GET(tc.path, func(c *gin.Context) {
				if tc.fail {
					_ = c.Error(errors.New("failed"))
				}
				c.Status(tc.code)
			})
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("status=%d", rec.Code)
			}
			if rec.Header().Get("traceparent") == "" {
				t.Fatal("traceparent not returned")
			}
		})
	}
}
