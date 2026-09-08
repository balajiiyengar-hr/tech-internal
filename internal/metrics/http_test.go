package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tech-internal/internal/apierr"

	"github.com/gin-gonic/gin"
)

func TestMiddlewareUsesRouteTemplateAndGroupsErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	router.GET("/api/v1/users/:id", func(c *gin.Context) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid user-specific secret"})
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/users/12345", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	metricsResponse := httptest.NewRecorder()
	Handler().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()

	wantLabels := `custom_error_code="",error_group="unauthorized",method="GET",route="/api/v1/users/:id",service="",status_class="4xx"`
	if !strings.Contains(body, `tech_internal_api_http_requests_total{`+wantLabels+`} 1`) {
		t.Fatalf("request counter does not contain expected labels")
	}
	if strings.Contains(body, "12345") || strings.Contains(body, "invalid user-specific secret") {
		t.Fatal("metrics contain a raw identifier or free-text error")
	}
	if !strings.Contains(body, `tech_internal_api_http_request_duration_seconds_bucket{`+wantLabels+`,le="`) {
		t.Fatal("duration histogram buckets are missing")
	}
}

func TestMiddlewarePublishesServiceAndCustomErrorCodeLabels(t *testing.T) {
	SetServiceName("tech-internal-api-test")
	defer SetServiceName("")

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	router.GET("/api/v1/domain-check", func(c *gin.Context) {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationError, "organization is required")
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/domain-check", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}

	metricsResponse := httptest.NewRecorder()
	Handler().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()

	wantLabels := `custom_error_code="VALIDATION_ERROR",error_group="validation",method="GET",route="/api/v1/domain-check",service="tech-internal-api-test",status_class="4xx"`
	if !strings.Contains(body, `tech_internal_api_http_requests_total{`+wantLabels+`} 1`) {
		t.Fatalf("request counter does not contain expected service/custom_error_code labels:\n%s", body)
	}
}

func TestErrorGroup(t *testing.T) {
	tests := map[int]string{
		http.StatusOK:                  "none",
		http.StatusBadRequest:          "validation",
		http.StatusUnauthorized:        "unauthorized",
		http.StatusForbidden:           "forbidden",
		http.StatusNotFound:            "not_found",
		http.StatusConflict:            "conflict",
		http.StatusTooManyRequests:     "rate_limit",
		http.StatusBadGateway:          "upstream",
		http.StatusInternalServerError: "internal",
		http.StatusTeapot:              "other",
	}
	for status, want := range tests {
		if got := errorGroup(status); got != want {
			t.Errorf("errorGroup(%d) = %q, want %q", status, got, want)
		}
	}
}
