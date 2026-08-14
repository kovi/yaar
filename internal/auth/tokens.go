package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// APITokenPrefix marks a credential as an opaque API token rather than a JWT.
// Both are sent as `Authorization: Bearer`, and the middleware branches on this
// prefix to decide how to validate. Github-style prefix makes it easy to identify.
const APITokenPrefix = "af_"

// GenerateRandomToken returns a plain-text token with a prefix (e.g., af_...)
func GenerateRandomToken() (string, error) {
	b := make([]byte, 24) // 24 bytes of entropy
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s", APITokenPrefix, hex.EncodeToString(b)), nil
}

// HashToken converts a plain-text token into a SHA256 hex string
func HashToken(plainText string) string {
	hash := sha256.Sum256([]byte(plainText))
	return hex.EncodeToString(hash[:])
}
