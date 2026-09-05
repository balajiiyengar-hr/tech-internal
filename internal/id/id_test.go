package id

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewUUIDv7(t *testing.T) {
	value, err := NewUUIDv7()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Version() != 7 {
		t.Fatalf("version = %v", parsed.Version())
	}
}
