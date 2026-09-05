package id

import (
	"fmt"

	"github.com/google/uuid"
)

// NewUUIDv7 returns a time-ordered RFC 9562 UUIDv7.
func NewUUIDv7() (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate uuidv7: %w", err)
	}
	return value.String(), nil
}
