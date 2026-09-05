package auth

import (
	"strings"
	"testing"
	"time"

	"tech-internal/internal/config"
	"tech-internal/internal/models"
)

func TestSaltedSHA256Password(t *testing.T) {
	first, err := HashPasswordWithKDF("Admin@123", "sha256")
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPasswordWithKDF("Admin@123", "sha256")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, sha256Prefix) {
		t.Fatalf("unexpected format: %s", first)
	}
	if first == second {
		t.Fatal("password hashes must use unique salts")
	}
	if !CheckPassword(first, "Admin@123") {
		t.Fatal("correct password did not verify")
	}
	if CheckPassword(first, "wrong") {
		t.Fatal("wrong password verified")
	}
}

func TestArgon2PasswordStillVerifies(t *testing.T) {
	hash, err := HashPassword("Admin@123")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(hash, "Admin@123") {
		t.Fatal("existing Argon2id hash did not verify")
	}
}

func BenchmarkArgon2idHash(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := HashPassword("Admin@123"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkArgon2idVerify(b *testing.B) {
	encoded, err := HashPassword("Admin@123")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !CheckPassword(encoded, "Admin@123") {
			b.Fatal("verify failed")
		}
	}
}

func BenchmarkJWTIssuePair(b *testing.B) {
	svc := NewTokenService(config.Config{
		JWTSecret:     "tech-internal-dev-secret-change-in-production",
		JWTExpiry:     24 * time.Hour,
		RefreshExpiry: 96 * time.Hour,
	})
	user := &models.User{
		Domain:      "techhr.com",
		Type:        models.UserTypeEmail,
		Identifier:  "admin@techhr.com",
		Role:        models.RoleAdmin,
		DisplayName: "Portal Admin",
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := svc.IssuePair(user, ""); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJWTParse(b *testing.B) {
	svc := NewTokenService(config.Config{
		JWTSecret:     "tech-internal-dev-secret-change-in-production",
		JWTExpiry:     24 * time.Hour,
		RefreshExpiry: 96 * time.Hour,
	})
	user := &models.User{
		Domain:      "techhr.com",
		Type:        models.UserTypeEmail,
		Identifier:  "admin@techhr.com",
		Role:        models.RoleAdmin,
		DisplayName: "Portal Admin",
	}
	pair, err := svc.IssuePair(user, "")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Parse(pair.AccessToken); err != nil {
			b.Fatal(err)
		}
	}
}
