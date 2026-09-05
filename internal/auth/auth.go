package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"tech-internal/internal/config"
	appcrypto "tech-internal/internal/crypto"
	"tech-internal/internal/models"
	"tech-internal/pkg/portalauth"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

// Argon2id parameters follow OWASP guidance for interactive logins
// while remaining practical at ~100 TPS with a connection pool.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // 19 MiB
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
	sha256Prefix = "$sha256$v=1$"
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return encodeArgon2id(salt, hash), nil
}

func CheckPassword(encodedHash, password string) bool {
	if strings.HasPrefix(encodedHash, sha256Prefix) {
		return checkSaltedSHA256(encodedHash, password)
	}
	salt, expected, timeCost, memory, threads, keyLen, err := decodeArgon2id(encodedHash)
	if err != nil {
		return false
	}

	actual := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, keyLen)
	if len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func HashPasswordWithKDF(password, kdf string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(kdf), "sha256") {
		return hashSaltedSHA256(password)
	}
	return HashPassword(password)
}

func hashSaltedSHA256(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	sum := sha256.Sum256(append(append([]byte(nil), salt...), []byte(password)...))
	return sha256Prefix +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(sum[:]), nil
}

func checkSaltedSHA256(encoded, password string) bool {
	parts := strings.Split(strings.TrimPrefix(encoded, sha256Prefix), "$")
	if len(parts) != 2 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	sum := sha256.Sum256(append(append([]byte(nil), salt...), []byte(password)...))
	return subtle.ConstantTimeCompare(sum[:], expected) == 1
}

func IsArgon2id(encodedHash string) bool {
	return strings.HasPrefix(encodedHash, "$argon2id$")
}

func ProtectSecret(key []byte, secret string) (string, error) {
	hash, err := HashPassword(secret)
	if err != nil {
		return "", err
	}
	return appcrypto.Seal(key, hash)
}

func ProtectPassword(key []byte, password, kdf string) (string, error) {
	hash, err := HashPasswordWithKDF(password, kdf)
	if err != nil {
		return "", err
	}
	return appcrypto.Seal(key, hash)
}

// ProtectOTP uses authenticated encryption only. OTPs are random, short-lived,
// and rate-limited; a memory-hard password KDF only burns capacity here.
func ProtectOTP(key []byte, otp string) (string, error) {
	return appcrypto.Seal(key, otp)
}

func OpenOTP(key []byte, protected string) (string, error) {
	return appcrypto.Open(key, protected)
}

func VerifySecret(key []byte, stored, secret string) bool {
	hash, err := appcrypto.Open(key, stored)
	if err != nil {
		return false
	}
	return CheckPassword(hash, secret)
}

func IsProtected(stored string) bool {
	return appcrypto.IsSealed(stored)
}

func GenerateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func encodeArgon2id(salt, hash []byte) string {
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64Salt, b64Hash)
}

func decodeArgon2id(encoded string) (salt, hash []byte, timeCost, memory uint32, threads uint8, keyLen uint32, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid argon2id hash")
	}

	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid argon2id version")
	}
	if version != argon2.Version {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("unsupported argon2 version")
	}

	var memoryInt, timeInt, threadsInt int
	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memoryInt, &timeInt, &threadsInt); err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid argon2id parameters")
	}
	if memoryInt <= 0 || timeInt <= 0 || threadsInt <= 0 || threadsInt > 255 {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid argon2id parameters")
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid argon2id salt")
	}
	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid argon2id hash")
	}

	return salt, hash, uint32(timeInt), uint32(memoryInt), uint8(threadsInt), uint32(len(hash)), nil
}

type TokenService struct {
	secret        []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	AccessExp    time.Time
	RefreshExp   time.Time
	AccessJTI    string
	RefreshJTI   string
	FamilyID     string
}

func NewTokenService(cfg config.Config) *TokenService {
	return &TokenService{
		secret:        []byte(cfg.JWTSecret),
		accessExpiry:  cfg.JWTExpiry,
		refreshExpiry: cfg.RefreshExpiry,
	}
}

func NewJTI() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%x", buf)
}

func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *TokenService) Issue(user *models.User) (string, time.Time, error) {
	pair, err := s.IssuePair(user, "")
	if err != nil {
		return "", time.Time{}, err
	}
	return pair.AccessToken, pair.AccessExp, nil
}

func (s *TokenService) IssuePair(user *models.User, familyID string) (*TokenPair, error) {
	if familyID == "" {
		familyID = NewJTI()
	}
	now := time.Now()
	accessJTI := NewJTI()
	refreshJTI := NewJTI()
	accessExp := now.Add(s.accessExpiry)
	refreshExp := now.Add(s.refreshExpiry)

	access, err := s.sign(user, models.TokenKindAccess, accessJTI, familyID, accessExp, now)
	if err != nil {
		return nil, err
	}
	refresh, err := s.sign(user, models.TokenKindRefresh, refreshJTI, familyID, refreshExp, now)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		AccessExp:    accessExp,
		RefreshExp:   refreshExp,
		AccessJTI:    accessJTI,
		RefreshJTI:   refreshJTI,
		FamilyID:     familyID,
	}, nil
}

func (s *TokenService) sign(user *models.User, use, jti, familyID string, exp, iat time.Time) (string, error) {
	claims := jwt.MapClaims{
		"person_id":       user.PersonID,
		"organization_id": user.OrganizationID,
		"org_slug":        user.Domain,
		"identity_id":     user.IdentityID,
		"roles":           user.Roles,
		"permissions":     user.Permissions,
		"domain":          user.Domain,
		"type":            user.Type,
		"identifier":      user.Identifier,
		"role":            user.Role,
		"display_name":    user.DisplayName,
		"sub":             user.MembershipID,
		"scopes":          user.Permissions,
		"aud":             portalauth.Audience,
		"jti":             jti,
		"token_use":       use,
		"family_id":       familyID,
		"exp":             exp.Unix(),
		"iat":             iat.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

func (s *TokenService) Parse(tokenStr string) (*models.Claims, error) {
	pc, err := portalauth.Authenticate(s.secret, tokenStr)
	if err != nil {
		return nil, err
	}
	return mapClaims(pc), nil
}

func (s *TokenService) ParseRefresh(tokenStr string) (*models.Claims, error) {
	pc, err := portalauth.ParseSigned(s.secret, tokenStr)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(pc.TokenUse, portalauth.TokenUseRefresh) {
		return nil, portalauth.ErrWrongUse
	}
	return mapClaims(pc), nil
}

func (s *TokenService) AccessTTL() time.Duration  { return s.accessExpiry }
func (s *TokenService) RefreshTTL() time.Duration { return s.refreshExpiry }

func mapClaims(pc *portalauth.Claims) *models.Claims {
	return &models.Claims{
		UserID:           pc.UserID,
		PersonID:         pc.PersonID,
		OrganizationID:   pc.OrganizationID,
		OrganizationSlug: pc.OrganizationSlug,
		IdentityID:       pc.IdentityID,
		Domain:           pc.Domain,
		Type:             pc.Type,
		Identifier:       pc.Identifier,
		Role:             pc.Role,
		Roles:            pc.Roles,
		Permissions:      pc.Permissions,
		Display:          pc.Display,
		Scopes:           pc.EffectiveScopes(),
		JTI:              pc.JTI,
		TokenUse:         pc.TokenUse,
		FamilyID:         pc.FamilyID,
	}
}
