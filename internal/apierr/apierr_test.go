package apierr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestJSONWritesStandardBodyAndContextKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	JSON(c, http.StatusBadRequest, ValidationError, "domain is required")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body["error"] != "domain is required" {
		t.Fatalf("error = %v, want %q", body["error"], "domain is required")
	}
	if body["custom_error_code"] != string(ValidationError) {
		t.Fatalf("custom_error_code = %v, want %q", body["custom_error_code"], ValidationError)
	}
	if got := c.GetString(ContextKey); got != string(ValidationError) {
		t.Fatalf("context key = %q, want %q", got, ValidationError)
	}
}

func TestAbortWritesStandardBodyAndAborts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	Abort(c, http.StatusUnauthorized, AuthMissingToken, "missing authorization header")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if !c.IsAborted() {
		t.Fatal("context was not aborted")
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body["custom_error_code"] != string(AuthMissingToken) {
		t.Fatalf("custom_error_code = %v, want %q", body["custom_error_code"], AuthMissingToken)
	}
	if got := c.GetString(ContextKey); got != string(AuthMissingToken) {
		t.Fatalf("context key = %q, want %q", got, AuthMissingToken)
	}
}
