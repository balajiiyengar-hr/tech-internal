package metrics

import (
	"net/http"
	"strconv"
	"time"

	"tech-internal/internal/apierr"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// httpLabels is shared by both HTTP metrics so the two stay in lockstep.
// custom_error_code is the bounded apierr.Code enum (empty for 2xx/3xx); service
// is the constant tag set once via SetServiceName, letting a shared Grafana/
// Prometheus instance group and filter dashboards per service.
var httpLabels = []string{"route", "method", "status_class", "error_group", "service", "custom_error_code"}

var (
	httpRequests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tech_internal_api",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total API HTTP requests.",
		},
		httpLabels,
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tech_internal_api",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "API HTTP request duration in seconds.",
			Buckets: []float64{
				0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01,
				0.025, 0.05, 0.1, 0.25, 0.5, 1,
			},
		},
		httpLabels,
	)
)

// Middleware records low-cardinality HTTP metrics using Gin route templates.
// /metrics is excluded so Prometheus scrapes do not affect API traffic panels.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		labels := []string{
			route,
			c.Request.Method,
			statusClass(status),
			errorGroup(status),
			ServiceName(),
			c.GetString(apierr.ContextKey),
		}
		httpRequests.WithLabelValues(labels...).Inc()
		httpRequestDuration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
	}
}

// Handler exposes the default registry, including Go and process collectors.
func Handler() http.Handler {
	return promhttp.Handler()
}

func statusClass(status int) string {
	return strconv.Itoa(status/100) + "xx"
}

func errorGroup(status int) string {
	switch {
	case status < http.StatusBadRequest:
		return "none"
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return "validation"
	case status == http.StatusUnauthorized:
		return "unauthorized"
	case status == http.StatusForbidden:
		return "forbidden"
	case status == http.StatusNotFound:
		return "not_found"
	case status == http.StatusConflict:
		return "conflict"
	case status == http.StatusTooManyRequests:
		return "rate_limit"
	case status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout:
		return "upstream"
	case status >= http.StatusInternalServerError:
		return "internal"
	default:
		return "other"
	}
}
