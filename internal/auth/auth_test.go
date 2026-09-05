package auth

import (
	"strings"
	"testing"
	"time"

	"tech-internal/internal/config"
	"tech-internal/internal/models"
	"tech-internal/pkg/portalauth"
)

func TestPasswordAndProtectedSecretP0(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	for _, kdf := range []string{"sha256", "argon2id"} {
		t.Run(kdf, func(t *testing.T) {
			hash, err := HashPasswordWithKDF("correct horse", kdf)
			if err != nil {
				t.Fatal(err)
			}
			if !CheckPassword(hash, "correct horse") || CheckPassword(hash, "wrong") {
				t.Fatal("password verification result is incorrect")
			}
		})
	}

	protected, err := ProtectSecret(key, "top-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !IsProtected(protected) || !VerifySecret(key, protected, "top-secret") {
		t.Fatal("protected secret did not verify")
	}
	if VerifySecret(key, protected, "wrong") || VerifySecret(key, "enc:v1:not-base64", "top-secret") {
		t.Fatal("invalid protected secret verified")
	}
}

func TestOTPProtectionAndGeneration(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	protected, err := ProtectOTP(key, "123456")
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenOTP(key, protected)
	if err != nil || got != "123456" {
		t.Fatalf("OpenOTP=%q,%v", got, err)
	}
	code, err := GenerateOTP()
	if err != nil || len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		t.Fatalf("GenerateOTP=%q,%v", code, err)
	}
}

func TestTokenServiceRoundTrip(t *testing.T) {
	service := NewTokenService(config.Config{
		JWTSecret: "unit-test-secret", JWTExpiry: time.Hour, RefreshExpiry: 24 * time.Hour,
	})
	user := &models.User{
		PersonID: "person", MembershipID: "member", OrganizationID: "org",
		IdentityID: "identity", Domain: "acme", Type: "email",
		Identifier: "a@b.test", Role: "admin", Roles: []string{"org_admin"},
		Permissions: []string{"members:read"}, DisplayName: "A",
	}
	pair, err := service.IssuePair(user, "family")
	if err != nil {
		t.Fatal(err)
	}
	if pair.FamilyID != "family" || pair.AccessJTI == pair.RefreshJTI {
		t.Fatalf("bad pair: %#v", pair)
	}
	claims, err := service.Parse(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != "member" || claims.JTI != pair.AccessJTI || claims.TokenUse != "access" {
		t.Fatalf("bad access claims: %#v", claims)
	}
	refresh, err := service.ParseRefresh(pair.RefreshToken)
	if err != nil || refresh.TokenUse != "refresh" {
		t.Fatalf("bad refresh claims: %#v %v", refresh, err)
	}
	if _, err := service.ParseRefresh(pair.AccessToken); err != portalauth.ErrWrongUse {
		t.Fatalf("access accepted as refresh: %v", err)
	}
	raw, exp, err := service.Issue(user)
	if err != nil || raw == "" || exp.IsZero() {
		t.Fatalf("Issue = %q %v %v", raw, exp, err)
	}
	if service.AccessTTL() != time.Hour || service.RefreshTTL() != 24*time.Hour {
		t.Fatal("TTL getters failed")
	}
	if len(NewJTI()) != 32 || len(HashToken("token")) != 64 {
		t.Fatal("identifier/hash lengths are wrong")
	}
	if !IsArgon2id("$argon2id$anything") || IsArgon2id("$sha256$anything") {
		t.Fatal("argon detection failed")
	}
}

func TestMalformedPasswordHashes(t *testing.T) {
	badArgon := []string{
		"", "$argon2i$v=19$m=1,t=1,p=1$YQ$Yg",
		"$argon2id$v=x$m=1,t=1,p=1$YQ$Yg",
		"$argon2id$v=18$m=1,t=1,p=1$YQ$Yg",
		"$argon2id$v=19$bad$YQ$Yg",
		"$argon2id$v=19$m=0,t=1,p=1$YQ$Yg",
		"$argon2id$v=19$m=1,t=1,p=1$*$Yg",
		"$argon2id$v=19$m=1,t=1,p=1$YQ$*",
	}
	for _, value := range badArgon {
		if CheckPassword(value, "password") {
			t.Fatalf("accepted malformed hash %q", value)
		}
	}
	for _, value := range []string{
		"$sha256$v=1$one", "$sha256$v=1$*$YQ", "$sha256$v=1$YQ$*",
	} {
		if CheckPassword(value, "password") {
			t.Fatalf("accepted malformed sha hash %q", value)
		}
	}
}

func TestProtectPasswordAndTokenParseErrors(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	protected, err := ProtectPassword(key, "password", "sha256")
	if err != nil || !VerifySecret(key, protected, "password") {
		t.Fatalf("ProtectPassword=%q,%v", protected, err)
	}
	if _, err := ProtectPassword([]byte("short"), "password", "sha256"); err == nil {
		t.Fatal("invalid encryption key accepted")
	}
	service := NewTokenService(config.Config{JWTSecret: "secret", JWTExpiry: time.Hour, RefreshExpiry: time.Hour})
	if _, err := service.Parse("bad"); err == nil {
		t.Fatal("invalid access token accepted")
	}
	if _, err := service.ParseRefresh("bad"); err == nil {
		t.Fatal("invalid refresh token accepted")
	}
}
