package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPaginationDefaults(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)

	pageSize, page := pagination(c)
	if pageSize != 25 || page != 1 {
		t.Fatalf("pagination() = (%d, %d), want (25, 1)", pageSize, page)
	}
}

func TestPaginationClampsPageSize(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?pageSize=100&page=3", nil)

	pageSize, page := pagination(c)
	if pageSize != 60 || page != 3 {
		t.Fatalf("pagination() = (%d, %d), want (60, 3)", pageSize, page)
	}
}

func TestPaginationSupportsLegacyNames(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?PageSize=40&PageVal=2", nil)

	pageSize, page := pagination(c)
	if pageSize != 40 || page != 2 {
		t.Fatalf("pagination() = (%d, %d), want (40, 2)", pageSize, page)
	}
}
