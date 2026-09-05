package crypto

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestParseSealOpen(t *testing.T) {
	raw := []byte("0123456789abcdef0123456789abcdef")
	keys := []string{"", "passphrase", hex.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw)}
	for _, input := range keys {
		key := ParseKey(input)
		if len(key) != 32 {
			t.Fatalf("ParseKey(%q) length=%d", input, len(key))
		}
		sealed, err := Seal(key, "secret")
		if err != nil || !IsSealed(sealed) {
			t.Fatalf("Seal=%q,%v", sealed, err)
		}
		opened, err := Open(key, sealed)
		if err != nil || opened != "secret" {
			t.Fatalf("Open=%q,%v", opened, err)
		}
	}
	if got, err := Open(raw, "plaintext"); err != nil || got != "plaintext" {
		t.Fatalf("plain Open=%q,%v", got, err)
	}
	for _, bad := range []string{"enc:v1:not-base64", "enc:v1:YQ"} {
		if _, err := Open(raw, bad); err == nil {
			t.Fatalf("Open(%q) unexpectedly succeeded", bad)
		}
	}
	if _, err := Seal([]byte("short"), "secret"); err == nil {
		t.Fatal("Seal accepted invalid key")
	}
	sealed, err := Seal(raw, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open([]byte("short"), sealed); err == nil {
		t.Fatal("Open accepted invalid key")
	}
	wrong := []byte("abcdef0123456789abcdef0123456789")
	if _, err := Open(wrong, sealed); err == nil {
		t.Fatal("Open accepted wrong key")
	}
}
