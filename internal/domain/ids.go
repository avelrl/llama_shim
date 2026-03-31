package domain

import (
	"fmt"

	"github.com/google/uuid"
)

func NewPrefixedID(prefix string) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate uuidv7: %w", err)
	}
	return prefix + "_" + id.String(), nil
}

func MustNewRequestID() string {
	id, err := NewPrefixedID("req")
	if err != nil {
		return "req_unknown"
	}
	return id
}
