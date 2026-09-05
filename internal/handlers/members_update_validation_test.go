package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"tech-internal/internal/repository"
)

func TestUpdateMemberRoleValidationResponses(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantError string
	}{
		{
			name:      "unknown role",
			err:       fmt.Errorf("%w %q", repository.ErrUnknownRole, "unavailable"),
			wantError: `unknown role \"unavailable\"`,
		},
		{
			name:      "empty roles",
			err:       repository.ErrInvalidRoleAssignment,
			wantError: repository.ErrInvalidRoleAssignment.Error(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{members: &p0Members{updateErr: tt.err}}
			rec := memberContext(t, http.MethodPatch, "/", `{"role_keys":["member"]}`, h.UpdateMember)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want=400 body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantError) {
				t.Fatalf("body=%s, want error containing %q", rec.Body.String(), tt.wantError)
			}
		})
	}
}
