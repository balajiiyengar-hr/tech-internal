package middleware

import (
	"strings"
	"time"

	"tech-internal/internal/fmtlog"
	"tech-internal/internal/models"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
)

const RequestIDKey = "request_id"
const RequestIDHeader = "X-Request-ID"

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader(RequestIDHeader))
		if id == "" {
			id = fmtlog.NewRequestID()
		}
		c.Set(RequestIDKey, id)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" || c.Request.URL.Path == "/api/v1/health" {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		level := "info"
		if status >= 500 {
			level = "error"
		} else if status >= 400 {
			level = "warn"
		}

		fields := map[string]any{
			"trace.id":                  TraceID(c),
			"span.id":                   SpanID(c),
			"http.request.id":           c.GetString(RequestIDKey),
			"http.request.method":       c.Request.Method,
			"url.path":                  c.Request.URL.Path,
			"url.query":                 c.Request.URL.RawQuery,
			"http.response.status_code": status,
			"event.duration":            time.Since(start).Nanoseconds(),
			"event.action":              "http_request",
			"client.ip":                 c.ClientIP(),
			"user_agent.original":       c.Request.UserAgent(),
			"http.response.body.bytes":  c.Writer.Size(),
		}

		if v, ok := c.Get("claims"); ok {
			if claims, ok := v.(*models.Claims); ok {
				fields["user.domain"] = claims.Domain
				fields["user.id"] = claims.Identifier
				fields["user.roles"] = claims.Role
			}
		}

		fmtlog.Default().Log(level, "http_request", fields)
	}
}

func TraceID(c *gin.Context) string {
	spanContext := trace.SpanContextFromContext(c.Request.Context())
	if spanContext.IsValid() {
		return spanContext.TraceID().String()
	}
	return c.GetString(RequestIDKey)
}

func SpanID(c *gin.Context) string {
	spanContext := trace.SpanContextFromContext(c.Request.Context())
	if spanContext.IsValid() {
		return spanContext.SpanID().String()
	}
	return ""
}
