package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"tech-internal/internal/auth"
	"tech-internal/internal/config"
	"tech-internal/internal/fmtlog"
	"tech-internal/internal/middleware"
	"tech-internal/internal/models"
	"tech-internal/internal/otp"
	"tech-internal/internal/repository"

	"github.com/gin-gonic/gin"
)

type OAuthTokenStore interface {
	InsertPair(ctx context.Context, access models.OAuthToken, accessHash string, refresh models.OAuthToken, refreshHash string) error
	RotatePair(ctx context.Context, oldRefreshJTI string, access models.OAuthToken, accessHash string, refresh models.OAuthToken, refreshHash string) (bool, error)
	GetActiveByJTI(ctx context.Context, jti string) (*models.OAuthToken, error)
	RevokeFamily(ctx context.Context, familyID string) (int64, error)
}

type userStore interface {
	Get(context.Context, string, string, string) (*models.User, error)
	ListByDomain(context.Context, string, int, int) ([]models.User, int64, error)
	Create(context.Context, models.User, *string) error
	UpdatePasswordHash(context.Context, string, string, string, string) error
}

type domainStore interface {
	Get(context.Context, string) (*models.DomainSettings, error)
	Update(context.Context, models.DomainSettings) error
}

type otpStore interface {
	Save(context.Context, string, string, string) error
	VerifyAndConsume(context.Context, string, string, string) error
}

type appStore interface {
	ListByDomain(context.Context, string, int, int) ([]models.PortalApp, int64, error)
	Create(context.Context, *models.PortalApp) error
	Delete(context.Context, string) error
}

type memberStore interface {
	userDeleter
	List(context.Context, string, int, int) ([]models.Member, int64, error)
	Get(context.Context, string, string) (*models.Member, error)
	Create(context.Context, string, *models.Member) error
	Update(context.Context, string, string, string, *bool, []string) error
	Delete(context.Context, string, string) error
	ListRoles(context.Context, string, int, int) ([]models.OrganizationRole, int64, error)
	CreateRole(context.Context, string, string, string, []string) (*models.OrganizationRole, error)
}

// userDeleter resolves and removes a user from one login identity. Splitting it
// out of MemberRepository keeps AdminDeleteUser's status mapping unit testable.
type userDeleter interface {
	DeleteByIdentity(ctx context.Context, organizationID, domain, userType, identifier string) error
}

type Handler struct {
	cfg         config.Config
	users       userStore
	domains     domainStore
	otps        otpStore
	apps        appStore
	members     memberStore
	userDeleter userDeleter
	oauth       OAuthTokenStore
	tokens      *auth.TokenService
}

func New(
	cfg config.Config,
	users userStore,
	domains domainStore,
	otps otpStore,
	apps appStore,
	members memberStore,
	oauth OAuthTokenStore,
	tokens *auth.TokenService,
) *Handler {
	return &Handler{
		cfg: cfg, users: users, domains: domains, otps: otps, apps: apps,
		members: members, userDeleter: members, oauth: oauth, tokens: tokens,
	}
}

type emailLoginRequest struct {
	Domain       string `json:"domain"`
	Organization string `json:"organization"`
	Portal       string `json:"portal"`
	Identifier   string `json:"identifier" binding:"required"`
	Password     string `json:"password" binding:"required"`
}

type otpRequestRequest struct {
	Domain       string `json:"domain"`
	Organization string `json:"organization"`
	Portal       string `json:"portal"`
	Identifier   string `json:"identifier" binding:"required"`
}

type otpVerifyRequest struct {
	Domain       string `json:"domain"`
	Organization string `json:"organization"`
	Portal       string `json:"portal"`
	Identifier   string `json:"identifier" binding:"required"`
	OTP          string `json:"otp" binding:"required"`
}

type oauthRefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type registerUserRequest struct {
	Domain      string `json:"domain" binding:"required"`
	Type        string `json:"type" binding:"required"`
	Identifier  string `json:"identifier" binding:"required"`
	Password    string `json:"password"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name"`
}

type createAppRequest struct {
	Section   string `json:"section"`
	Name      string `json:"name" binding:"required"`
	URL       string `json:"url" binding:"required"`
	IconURL   string `json:"icon_url"`
	SortOrder int    `json:"sort_order"`
}

type resetPasswordRequest struct {
	Password string `json:"password" binding:"required"`
}

type updateDomainRequest struct {
	EmailLoginEnabled *bool `json:"email_login_enabled"`
	SMSLoginEnabled   *bool `json:"sms_login_enabled"`
}

type createMemberRequest struct {
	DisplayName string   `json:"display_name" binding:"required"`
	RoleKeys    []string `json:"role_keys" binding:"required"`
	Email       string   `json:"email"`
	Password    string   `json:"password"`
	Phone       string   `json:"phone"`
}

type updateMemberRequest struct {
	DisplayName string   `json:"display_name"`
	Active      *bool    `json:"active"`
	RoleKeys    []string `json:"role_keys"`
}

type createRoleRequest struct {
	Key         string   `json:"key" binding:"required"`
	Name        string   `json:"name" binding:"required"`
	Permissions []string `json:"permissions"`
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) OAuthConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"access_token_hours":        h.tokens.AccessTTL().Hours(),
		"access_token_ttl_seconds":  int64(h.tokens.AccessTTL().Seconds()),
		"refresh_token_hours":       h.tokens.RefreshTTL().Hours(),
		"refresh_token_ttl_seconds": int64(h.tokens.RefreshTTL().Seconds()),
		"token_endpoint":            "/api/v1/oauth/token",
		"refresh_endpoint":          "/api/v1/oauth/token/refresh",
		"logout_endpoint":           "/api/v1/auth/logout",
		"revoke_endpoint":           "/api/v1/oauth/revoke",
	})
}

func (h *Handler) OAuthToken(c *gin.Context) {
	var req emailLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain, identifier and password are required"})
		return
	}

	domain := loginOrganization(req.Domain, req.Organization)
	if domain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "organization is required"})
		return
	}
	identifier := strings.ToLower(strings.TrimSpace(req.Identifier))
	if !h.requireLoginMethod(c, domain, models.UserTypeEmail) {
		return
	}

	user, err := h.users.Get(c.Request.Context(), domain, models.UserTypeEmail, identifier)
	if err != nil || user.PasswordHash == nil ||
		!auth.VerifySecret(h.cfg.EncryptionKey, *user.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if !user.Active {
		c.JSON(http.StatusForbidden, gin.H{"error": "account is disabled"})
		return
	}
	if !enforcePortal(c, user, req.Portal) {
		return
	}
	h.issueToken(c, user)
}

func (h *Handler) OAuthRefresh(c *gin.Context) {
	var req oauthRefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh_token is required"})
		return
	}

	claims, err := h.tokens.ParseRefresh(strings.TrimSpace(req.RefreshToken))
	if err != nil {
		h.refreshUnauthorized(c)
		return
	}
	active, err := h.oauth.GetActiveByJTI(c.Request.Context(), claims.JTI)
	if err != nil || active.Kind != models.TokenKindRefresh ||
		active.Domain != claims.Domain || active.Type != claims.Type ||
		active.Identifier != claims.Identifier ||
		(active.MembershipID != "" && active.MembershipID != claims.UserID) ||
		(active.IdentityID != "" && active.IdentityID != claims.IdentityID) ||
		(active.OrganizationID != "" && active.OrganizationID != claims.OrganizationID) {
		h.refreshUnauthorized(c)
		return
	}

	user, err := h.users.Get(
		c.Request.Context(), claims.Domain, claims.Type, claims.Identifier,
	)
	if err != nil || !user.Active {
		h.refreshUnauthorized(c)
		return
	}

	pair, err := h.tokens.IssuePair(user, claims.FamilyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not issue token"})
		return
	}
	ok, err := h.oauth.RotatePair(
		c.Request.Context(),
		claims.JTI,
		oauthRecord(user, pair, models.TokenKindAccess),
		auth.HashToken(pair.AccessToken),
		oauthRecord(user, pair, models.TokenKindRefresh),
		auth.HashToken(pair.RefreshToken),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not rotate token"})
		return
	}
	if !ok {
		h.refreshUnauthorized(c)
		return
	}
	c.JSON(http.StatusOK, tokenPairResponse(h.tokens, user, pair))
}

func (h *Handler) refreshUnauthorized(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{
		"error": "refresh token is invalid, expired, or revoked; please sign in again",
	})
}

func (h *Handler) Logout(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	if claims.FamilyID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session token"})
		return
	}
	if _, err := h.oauth.RevokeFamily(c.Request.Context(), claims.FamilyID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not revoke session"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) GetDomainSettings(c *gin.Context) {
	domain := strings.ToLower(strings.TrimSpace(c.Query("domain")))
	if domain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain is required"})
		return
	}
	settings, err := h.domains.Get(c.Request.Context(), domain)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "unknown domain"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load domain"})
		return
	}
	c.JSON(http.StatusOK, settings)
}

func (h *Handler) LoginEmail(c *gin.Context) {
	var req emailLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain, identifier and password are required"})
		return
	}

	domain := loginOrganization(req.Domain, req.Organization)
	if domain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "organization is required"})
		return
	}
	identifier := strings.ToLower(strings.TrimSpace(req.Identifier))

	if !h.requireLoginMethod(c, domain, models.UserTypeEmail) {
		return
	}

	user, err := h.users.Get(c.Request.Context(), domain, models.UserTypeEmail, identifier)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "login failed"})
		return
	}

	if !user.Active {
		c.JSON(http.StatusForbidden, gin.H{"error": "account is disabled"})
		return
	}
	if user.PasswordHash == nil || !auth.VerifySecret(h.cfg.EncryptionKey, *user.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if !enforcePortal(c, user, req.Portal) {
		return
	}

	h.issueToken(c, user)
}

func (h *Handler) RequestOTP(c *gin.Context) {
	var req otpRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain and identifier are required"})
		return
	}

	domain := loginOrganization(req.Domain, req.Organization)
	if domain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "organization is required"})
		return
	}
	identifier := strings.TrimSpace(req.Identifier)

	if !h.requireLoginMethod(c, domain, models.UserTypeSMS) {
		return
	}

	user, err := h.users.Get(c.Request.Context(), domain, models.UserTypeSMS, identifier)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "mobile number not registered"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "otp request failed"})
		return
	}
	if !user.Active {
		c.JSON(http.StatusForbidden, gin.H{"error": "account is disabled"})
		return
	}
	if !enforcePortal(c, user, req.Portal) {
		return
	}

	code, err := auth.GenerateOTP()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not generate otp"})
		return
	}

	protected, err := auth.ProtectOTP(h.cfg.EncryptionKey, code)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not generate otp"})
		return
	}

	if err := h.otps.Save(c.Request.Context(), domain, identifier, protected); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not store otp"})
		return
	}

	fields := map[string]any{
		"event.action":    "otp_request",
		"user.domain":     domain,
		"user.id":         identifier,
		"otp.ttl_seconds": int(h.cfg.OTPExpiry.Seconds()),
		"trace.id":        middleware.TraceID(c),
		"span.id":         middleware.SpanID(c),
	}
	if h.cfg.DevLogOTP {
		fields["otp.dev_code"] = code
	}
	fmtlog.Info("otp_issued", fields)

	resp := gin.H{
		"message":    "otp sent",
		"expires_in": int(h.cfg.OTPExpiry.Seconds()),
	}
	if h.cfg.DevLogOTP {
		resp["dev_code"] = code
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) VerifyOTP(c *gin.Context) {
	var req otpVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain, identifier and otp are required"})
		return
	}

	domain := loginOrganization(req.Domain, req.Organization)
	if domain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "organization is required"})
		return
	}
	identifier := strings.TrimSpace(req.Identifier)
	code := strings.TrimSpace(req.OTP)

	if !h.requireLoginMethod(c, domain, models.UserTypeSMS) {
		return
	}

	user, err := h.users.Get(c.Request.Context(), domain, models.UserTypeSMS, identifier)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid otp"})
		return
	}
	if !user.Active {
		c.JSON(http.StatusForbidden, gin.H{"error": "account is disabled"})
		return
	}
	if !enforcePortal(c, user, req.Portal) {
		return
	}

	if err := h.otps.VerifyAndConsume(c.Request.Context(), domain, identifier, code); err != nil {
		if errors.Is(err, otp.ErrExpired) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "otp expired, please request a new one"})
			return
		}
		if errors.Is(err, otp.ErrMismatch) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid otp"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "otp verification failed"})
		return
	}

	h.issueToken(c, user)
}

func (h *Handler) Me(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	settings, err := h.domains.Get(c.Request.Context(), claims.Domain)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load domain"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"membership_id":       claims.UserID,
		"person_id":           claims.PersonID,
		"organization_id":     claims.OrganizationID,
		"organization":        claims.OrganizationSlug,
		"identity_id":         claims.IdentityID,
		"roles":               claims.Roles,
		"permissions":         claims.Permissions,
		"domain":              claims.Domain,
		"type":                claims.Type,
		"identifier":          claims.Identifier,
		"user_id":             claims.UserID,
		"role":                claims.Role,
		"display_name":        claims.Display,
		"scopes":              claims.Scopes,
		"email_login_enabled": settings.EmailLoginEnabled,
		"sms_login_enabled":   settings.SMSLoginEnabled,
	})
}

func (h *Handler) ListApps(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	pageSize, pageVal := pagination(c)
	apps, total, err := h.apps.ListByDomain(
		c.Request.Context(), claims.Domain, pageSize, (pageVal-1)*pageSize,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load apps"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"apps":       apps,
		"pagination": paginationResponse(pageSize, pageVal, total),
	})
}

func (h *Handler) AdminGetDomain(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	settings, err := h.domains.Get(c.Request.Context(), claims.Domain)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load domain"})
		return
	}
	c.JSON(http.StatusOK, settings)
}

func (h *Handler) AdminUpdateDomain(c *gin.Context) {
	var req updateDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid domain settings"})
		return
	}

	claims := c.MustGet("claims").(*models.Claims)
	current, err := h.domains.Get(c.Request.Context(), claims.Domain)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load domain"})
		return
	}

	if req.EmailLoginEnabled != nil {
		current.EmailLoginEnabled = *req.EmailLoginEnabled
	}
	if req.SMSLoginEnabled != nil {
		current.SMSLoginEnabled = *req.SMSLoginEnabled
	}
	if !current.EmailLoginEnabled && !current.SMSLoginEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one login method must be enabled"})
		return
	}

	if err := h.domains.Update(c.Request.Context(), *current); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update domain"})
		return
	}
	c.JSON(http.StatusOK, current)
}

func (h *Handler) AdminListUsers(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	pageSize, pageVal := pagination(c)
	users, total, err := h.users.ListByDomain(
		c.Request.Context(), claims.Domain, pageSize, (pageVal-1)*pageSize,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load users"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"users":      users,
		"pagination": paginationResponse(pageSize, pageVal, total),
	})
}

func pagination(c *gin.Context) (pageSize, pageVal int) {
	const (
		defaultPageSize = 25
		maxPageSize     = 60
	)
	pageSize = defaultPageSize
	pageVal = 1
	if value, err := strconv.Atoi(firstQuery(c, "pageSize", "PageSize")); err == nil && value > 0 {
		pageSize = value
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	if value, err := strconv.Atoi(firstQuery(c, "page", "PageVal")); err == nil && value > 0 {
		pageVal = value
	}
	return pageSize, pageVal
}

func firstQuery(c *gin.Context, names ...string) string {
	for _, name := range names {
		if value := c.Query(name); value != "" {
			return value
		}
	}
	return ""
}

func paginationResponse(pageSize, pageVal int, total int64) gin.H {
	return gin.H{
		"page_size": pageSize,
		"page_val":  pageVal,
		"total":     total,
		"has_next":  int64(pageVal*pageSize) < total,
	}
}

func (h *Handler) AdminRegisterUser(c *gin.Context) {
	var req registerUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid registration payload"})
		return
	}

	claims := c.MustGet("claims").(*models.Claims)
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain != claims.Domain {
		c.JSON(http.StatusForbidden, gin.H{"error": "admins can only register users for their domain"})
		return
	}

	userType := strings.ToLower(strings.TrimSpace(req.Type))
	if userType != models.UserTypeEmail && userType != models.UserTypeSMS {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type must be email or sms"})
		return
	}

	settings, err := h.domains.Get(c.Request.Context(), domain)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load domain"})
		return
	}
	if userType == models.UserTypeEmail && !settings.EmailLoginEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email login is not enabled for this domain"})
		return
	}
	if userType == models.UserTypeSMS && !settings.SMSLoginEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mobile otp login is not enabled for this domain"})
		return
	}

	identifier := strings.TrimSpace(req.Identifier)
	if userType == models.UserTypeEmail {
		identifier = strings.ToLower(identifier)
	}

	role := strings.ToLower(strings.TrimSpace(req.Role))
	if role == "" {
		role = models.RoleUser
	}

	var passwordHash *string
	if userType == models.UserTypeEmail {
		if strings.TrimSpace(req.Password) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "password is required for email users"})
			return
		}
		hash, err := auth.ProtectPassword(h.cfg.EncryptionKey, req.Password, h.cfg.PasswordKDF)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not hash password"})
			return
		}
		passwordHash = &hash
	}

	user := models.User{
		Domain:      domain,
		Type:        userType,
		Identifier:  identifier,
		Role:        role,
		Active:      true,
		DisplayName: strings.TrimSpace(req.DisplayName),
	}

	if err := h.users.Create(c.Request.Context(), user, passwordHash); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "user already exists or could not be created"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "user registered",
		"user":    user,
	})
}

func (h *Handler) AdminResetPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is required"})
		return
	}

	password := strings.TrimSpace(req.Password)
	if len(password) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password must be at least 8 characters"})
		return
	}

	claims := c.MustGet("claims").(*models.Claims)
	userType := strings.ToLower(strings.TrimSpace(c.Param("type")))
	identifier := strings.TrimSpace(c.Param("identifier"))

	if userType != models.UserTypeEmail {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password reset is only available for email users"})
		return
	}
	identifier = strings.ToLower(identifier)

	user, err := h.users.Get(c.Request.Context(), claims.Domain, userType, identifier)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load user"})
		return
	}
	if !user.Active {
		c.JSON(http.StatusForbidden, gin.H{"error": "account is disabled"})
		return
	}

	hash, err := auth.ProtectPassword(h.cfg.EncryptionKey, password, h.cfg.PasswordKDF)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not hash password"})
		return
	}

	if err := h.users.UpdatePasswordHash(c.Request.Context(), claims.Domain, userType, identifier, hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not reset password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "password reset"})
}

func (h *Handler) AdminDeleteUser(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	userType := c.Param("type")
	identifier := c.Param("identifier")

	if userType != models.UserTypeEmail {
		identifier = strings.TrimSpace(identifier)
	} else {
		identifier = strings.ToLower(strings.TrimSpace(identifier))
	}

	if identifier == claims.Identifier && userType == claims.Type {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete your own account"})
		return
	}

	err := h.userDeleter.DeleteByIdentity(
		c.Request.Context(), claims.OrganizationID, claims.Domain, userType, identifier,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"message": "user deleted"})
	case errors.Is(err, repository.ErrNotFound):
		// Also covers a concurrent delete that won the race, so retries and
		// duplicate calls stay idempotent instead of surfacing as 5xx.
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
	case errors.Is(err, repository.ErrLastAdmin):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete user"})
	}
}

func (h *Handler) AdminLoginEmail(c *gin.Context)  { c.Set("login_portal", "admin"); h.LoginEmail(c) }
func (h *Handler) MemberLoginEmail(c *gin.Context) { c.Set("login_portal", "member"); h.LoginEmail(c) }
func (h *Handler) AdminOAuthToken(c *gin.Context)  { c.Set("login_portal", "admin"); h.OAuthToken(c) }
func (h *Handler) MemberOAuthToken(c *gin.Context) { c.Set("login_portal", "member"); h.OAuthToken(c) }

func loginOrganization(domain, organization string) string {
	if strings.TrimSpace(organization) != "" {
		return strings.ToLower(strings.TrimSpace(organization))
	}
	return strings.ToLower(strings.TrimSpace(domain))
}

func enforcePortal(c *gin.Context, user *models.User, requested string) bool {
	if requested == "" {
		requested = c.GetString("login_portal")
	}
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		return true
	} // legacy endpoint remains dual-issue compatible.
	isAdmin := false
	for _, role := range user.Roles {
		if role == models.RoleOrgAdmin {
			isAdmin = true
			break
		}
	}
	if requested == "admin" && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "this account is a member; use the member login"})
		return false
	}
	if requested == "member" && isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "this account is an org admin; use the org admin login"})
		return false
	}
	if requested != "admin" && requested != "member" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "portal must be admin or member"})
		return false
	}
	return true
}

func (h *Handler) ListMembers(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	if !sameOrganization(c, claims) {
		return
	}
	pageSize, pageVal := pagination(c)
	members, total, err := h.members.List(c.Request.Context(), claims.OrganizationID, pageSize, (pageVal-1)*pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load members"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": members, "pagination": paginationResponse(pageSize, pageVal, total)})
}

func (h *Handler) CreateMember(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	if !sameOrganization(c, claims) {
		return
	}
	var req createMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "display_name and role_keys are required"})
		return
	}
	member := models.Member{DisplayName: strings.TrimSpace(req.DisplayName), RoleKeys: req.RoleKeys}
	if email := strings.ToLower(strings.TrimSpace(req.Email)); email != "" {
		if len(req.Password) < 8 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "email identity requires a password of at least 8 characters"})
			return
		}
		hash, err := auth.ProtectPassword(h.cfg.EncryptionKey, req.Password, h.cfg.PasswordKDF)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not hash password"})
			return
		}
		member.Identities = append(member.Identities, models.IdentityInput{Type: models.UserTypeEmail, Identifier: email, Password: hash})
	}
	if phone := strings.TrimSpace(req.Phone); phone != "" {
		member.Identities = append(member.Identities, models.IdentityInput{Type: models.UserTypeSMS, Identifier: phone})
	}
	if len(member.Identities) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email or phone is required"})
		return
	}
	if err := h.members.Create(c.Request.Context(), claims.OrganizationID, &member); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": "email or phone is already assigned to an organization member"})
			return
		}
		if strings.Contains(err.Error(), "unknown role") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create member"})
		return
	}
	created, err := h.members.Get(c.Request.Context(), claims.OrganizationID, member.ID)
	if err == nil {
		member = *created
	}
	c.JSON(http.StatusCreated, gin.H{"message": "member created", "member": member})
}

func (h *Handler) UpdateMember(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	if !sameOrganization(c, claims) {
		return
	}
	var req updateMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid member update"})
		return
	}
	err := h.members.Update(c.Request.Context(), claims.OrganizationID, c.Param("member_id"), strings.TrimSpace(req.DisplayName), req.Active, req.RoleKeys)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return
	}
	if errors.Is(err, repository.ErrLastAdmin) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		if errors.Is(err, repository.ErrUnknownRole) ||
			errors.Is(err, repository.ErrInvalidRoleAssignment) ||
			strings.Contains(err.Error(), "unknown role") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update member"})
		}
		return
	}
	member, _ := h.members.Get(c.Request.Context(), claims.OrganizationID, c.Param("member_id"))
	c.JSON(http.StatusOK, gin.H{"member": member})
}

func (h *Handler) DeleteMember(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	if !sameOrganization(c, claims) {
		return
	}
	if c.Param("member_id") == claims.UserID {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot delete your own active session"})
		return
	}
	err := h.members.Delete(c.Request.Context(), claims.OrganizationID, c.Param("member_id"))
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return
	}
	if errors.Is(err, repository.ErrLastAdmin) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete member"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ListRoles(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	if !sameOrganization(c, claims) {
		return
	}
	pageSize, pageVal := pagination(c)
	roles, total, err := h.members.ListRoles(
		c.Request.Context(), claims.OrganizationID, pageSize, (pageVal-1)*pageSize,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load roles"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"roles":      roles,
		"pagination": paginationResponse(pageSize, pageVal, total),
	})
}

func (h *Handler) CreateRole(c *gin.Context) {
	claims := c.MustGet("claims").(*models.Claims)
	if !sameOrganization(c, claims) {
		return
	}
	var req createRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key and name are required"})
		return
	}
	key := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(req.Key)), " ", "_")
	role, err := h.members.CreateRole(c.Request.Context(), claims.OrganizationID, key, strings.TrimSpace(req.Name), req.Permissions)
	if errors.Is(err, repository.ErrConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": "role key already exists"})
		return
	}
	if err != nil {
		if strings.Contains(err.Error(), "unknown permission") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create role"})
		}
		return
	}
	c.JSON(http.StatusCreated, gin.H{"role": role})
}

func sameOrganization(c *gin.Context, claims *models.Claims) bool {
	id := strings.TrimSpace(c.Param("id"))
	if id != "" && id != claims.OrganizationID {
		c.JSON(http.StatusForbidden, gin.H{"error": "cross-organization access denied"})
		return false
	}
	return true
}

func (h *Handler) AdminCreateApp(c *gin.Context) {
	var req createAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and url are required"})
		return
	}

	claims := c.MustGet("claims").(*models.Claims)
	section := strings.TrimSpace(req.Section)
	if section == "" {
		section = "Applications"
	}

	app := models.PortalApp{
		Domain:    claims.Domain,
		Section:   section,
		Name:      strings.TrimSpace(req.Name),
		URL:       strings.TrimSpace(req.URL),
		IconURL:   strings.TrimSpace(req.IconURL),
		SortOrder: req.SortOrder,
		Active:    true,
	}

	if err := h.apps.Create(c.Request.Context(), &app); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create app"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "app created", "app": app})
}

func (h *Handler) AdminDeleteApp(c *gin.Context) {
	id := c.Param("id")
	if err := h.apps.Delete(c.Request.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "app not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not delete app"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "app deleted"})
}

func (h *Handler) requireLoginMethod(c *gin.Context, domain, userType string) bool {
	settings, err := h.domains.Get(c.Request.Context(), domain)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			return false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load domain"})
		return false
	}

	if userType == models.UserTypeEmail && !settings.EmailLoginEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "email login is not enabled for this domain"})
		return false
	}
	if userType == models.UserTypeSMS && !settings.SMSLoginEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "mobile otp login is not enabled for this domain"})
		return false
	}
	return true
}

func (h *Handler) issueToken(c *gin.Context, user *models.User) {
	body, err := h.issueTokenPair(c, user, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not issue token"})
		return
	}
	c.JSON(http.StatusOK, body)
}

func (h *Handler) issueTokenPair(
	c *gin.Context,
	user *models.User,
	familyID string,
) (gin.H, error) {
	pair, err := h.tokens.IssuePair(user, familyID)
	if err != nil {
		return nil, err
	}
	if err := h.oauth.InsertPair(
		c.Request.Context(),
		oauthRecord(user, pair, models.TokenKindAccess),
		auth.HashToken(pair.AccessToken),
		oauthRecord(user, pair, models.TokenKindRefresh),
		auth.HashToken(pair.RefreshToken),
	); err != nil {
		return nil, err
	}
	return tokenPairResponse(h.tokens, user, pair), nil
}

func oauthRecord(user *models.User, pair *auth.TokenPair, kind string) models.OAuthToken {
	jti, expiresAt := pair.AccessJTI, pair.AccessExp
	if kind == models.TokenKindRefresh {
		jti, expiresAt = pair.RefreshJTI, pair.RefreshExp
	}
	return models.OAuthToken{
		JTI:            jti,
		FamilyID:       pair.FamilyID,
		Domain:         user.Domain,
		Type:           user.Type,
		Identifier:     user.Identifier,
		MembershipID:   user.MembershipID,
		IdentityID:     user.IdentityID,
		OrganizationID: user.OrganizationID,
		Kind:           kind,
		ExpiresAt:      expiresAt,
	}
}

func tokenPairResponse(
	tokens *auth.TokenService,
	user *models.User,
	pair *auth.TokenPair,
) gin.H {
	return gin.H{
		"token_type":         "Bearer",
		"access_token":       pair.AccessToken,
		"refresh_token":      pair.RefreshToken,
		"expires_in":         int64(tokens.AccessTTL().Seconds()),
		"refresh_expires_in": int64(tokens.RefreshTTL().Seconds()),
		"expires_at":         pair.AccessExp,
		"refresh_expires_at": pair.RefreshExp,
		"token":              pair.AccessToken,
		"user": gin.H{
			"membership_id":   user.MembershipID,
			"person_id":       user.PersonID,
			"organization_id": user.OrganizationID,
			"organization":    user.Domain,
			"identity_id":     user.IdentityID,
			"domain":          user.Domain,
			"type":            user.Type,
			"identifier":      user.Identifier,
			"role":            user.Role,
			"roles":           user.Roles,
			"permissions":     user.Permissions,
			"display_name":    user.DisplayName,
		},
	}
}
