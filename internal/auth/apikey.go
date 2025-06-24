package auth

import (
	"errors"
	"strings"
)

var (
	ErrMissingAPIKey = errors.New("missing API key")
	ErrInvalidAPIKey = errors.New("invalid API key")
)

type APIKeyManager struct {
	internalAPIKey string
}

func NewAPIKeyManager(key string) *APIKeyManager {
	return &APIKeyManager{
		internalAPIKey: key,
	}
}

func (a *APIKeyManager) ValidateAPIKey(authHeader string) error {
	if authHeader == "" {
		return ErrMissingAPIKey
	}

	// Extract Bearer token
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return ErrInvalidAPIKey
	}

	apiKey := parts[1]
	if apiKey != a.internalAPIKey {
		return ErrInvalidAPIKey
	}

	return nil
}

func (a *APIKeyManager) ExtractAPIKey(authHeader string) string {
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) == 2 && parts[0] == "Bearer" {
		return parts[1]
	}
	return ""
}

// ValidateKey validates API key directly
func (a *APIKeyManager) ValidateKey(key string) bool {
	return key != "" && key == a.internalAPIKey
}
