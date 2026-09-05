package config

import (
	"testing"
	"time"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	t.Setenv("ACCESS_TOKEN_HOURS", "2")
	t.Setenv("REFRESH_TOKEN_HOURS", "8")
	t.Setenv("OTP_TTL_SECONDS", "45")
	t.Setenv("CORS_ORIGINS", " https://one.test/, https://two.test ")
	t.Setenv("DEV_LOG_OTP", "true")
	t.Setenv("PASSWORD_KDF", "SHA256")
	cfg := Load()
	if cfg.JWTExpiry != 2*time.Hour || cfg.RefreshExpiry != 8*time.Hour || cfg.OTPExpiry != 120*time.Second {
		t.Fatalf("unexpected durations: %+v", cfg)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[0] != "https://one.test" {
		t.Fatalf("origins=%v", cfg.CORSOrigins)
	}
	if !cfg.DevLogOTP || cfg.PasswordKDF != "sha256" {
		t.Fatalf("dev/kdf settings incorrect: %+v", cfg)
	}
}

func TestParseOriginsFallback(t *testing.T) {
	if got := parseOrigins(" , "); len(got) != 1 || got[0] != "*" {
		t.Fatalf("parseOrigins=%v", got)
	}
}

func TestLoadRepairsNonPositiveDurations(t *testing.T) {
	t.Setenv("ACCESS_TOKEN_HOURS", "0")
	t.Setenv("JWT_EXPIRY_HOURS", "0")
	t.Setenv("REFRESH_TOKEN_HOURS", "-1")
	cfg := Load()
	if cfg.JWTExpiry != 24*time.Hour || cfg.RefreshExpiry != 96*time.Hour {
		t.Fatalf("fallback durations: %+v", cfg)
	}
}
