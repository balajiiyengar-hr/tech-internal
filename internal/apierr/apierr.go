// Package apierr standardizes API error responses with a bounded
// custom_error_code enum. The code is grouped, low-cardinality, and safe to
// use as a Prometheus/log/trace label: it never carries free text, identifiers,
// or organization IDs.
package apierr

import "github.com/gin-gonic/gin"

// Code is a bounded, low-cardinality error classification safe to use as a
// metrics label and to group errors on a monitoring dashboard.
type Code string

// ContextKey is the gin.Context key the code is stashed under, and also the
// JSON field name returned to clients.
const ContextKey = "custom_error_code"

const (
	AuthMissingToken        Code = "AUTH_MISSING_TOKEN"
	AuthInvalidToken        Code = "AUTH_INVALID_TOKEN"
	AuthExpiredToken        Code = "AUTH_EXPIRED_TOKEN"
	AuthTokenRevoked        Code = "AUTH_TOKEN_REVOKED"
	AuthWrongTokenUse       Code = "AUTH_WRONG_TOKEN_USE"
	AuthInvalidCredentials  Code = "AUTH_INVALID_CREDENTIALS"
	AuthAccountDisabled     Code = "AUTH_ACCOUNT_DISABLED"
	AuthPortalMismatch      Code = "AUTH_PORTAL_MISMATCH"
	AuthLoginMethodDisabled Code = "AUTH_LOGIN_METHOD_DISABLED"
	AuthUnauthorized        Code = "AUTH_UNAUTHORIZED"

	OTPExpired           Code = "OTP_EXPIRED"
	OTPInvalid           Code = "OTP_INVALID"
	OTPUnknownIdentifier Code = "OTP_UNKNOWN_IDENTIFIER"

	ValidationError Code = "VALIDATION_ERROR"

	ForbiddenAdminRequired     Code = "FORBIDDEN_ADMIN_REQUIRED"
	ForbiddenPermissionDenied  Code = "FORBIDDEN_PERMISSION_DENIED"
	ForbiddenCrossOrganization Code = "FORBIDDEN_CROSS_ORGANIZATION"

	NotFound      Code = "NOT_FOUND"
	RouteNotFound Code = "ROUTE_NOT_FOUND"

	ConflictDuplicate Code = "CONFLICT_DUPLICATE"
	ConflictLastAdmin Code = "CONFLICT_LAST_ADMIN"
	SelfActionBlocked Code = "SELF_ACTION_BLOCKED"

	RoleUnknown           Code = "ROLE_UNKNOWN"
	RoleAssignmentInvalid Code = "ROLE_ASSIGNMENT_INVALID"
	PermissionUnknown     Code = "PERMISSION_UNKNOWN"

	InternalDBError     Code = "INTERNAL_DB_ERROR"
	InternalCacheError  Code = "INTERNAL_CACHE_ERROR"
	InternalTokenError  Code = "INTERNAL_TOKEN_ERROR"
	InternalCryptoError Code = "INTERNAL_CRYPTO_ERROR"
	InternalError       Code = "INTERNAL_ERROR"
)

// JSON writes the standardized error body and stashes the code on the
// context so metrics, access-log, and tracing middleware can read it after
// the handler returns.
func JSON(c *gin.Context, status int, code Code, message string) {
	c.Set(ContextKey, string(code))
	c.JSON(status, gin.H{"error": message, "custom_error_code": string(code)})
}

// Abort is JSON's AbortWithStatusJSON counterpart, for middleware that must
// stop the chain (auth failures, permission checks).
func Abort(c *gin.Context, status int, code Code, message string) {
	c.Set(ContextKey, string(code))
	c.AbortWithStatusJSON(status, gin.H{"error": message, "custom_error_code": string(code)})
}
