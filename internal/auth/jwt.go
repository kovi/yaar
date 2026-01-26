package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kovi/yaar/internal/models"
)

type Claims struct {
	UserID       uint              `json:"user_id"`
	Username     string            `json:"username"`
	IsAdmin      bool              `json:"is_admin"`
	AllowedPaths models.StringList `json:"allowed_paths"`
	jwt.RegisteredClaims
}

// GenerateToken creates a new JWT for a user that expires in 24 hours
func GenerateToken(user models.User, secret string) (string, error) {
	claims := &Claims{
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
		AllowedPaths: user.AllowedPaths,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ValidateToken parses the JWT string and returns the claims if valid
func ValidateToken(tokenStr string, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (any, error) {
		return []byte(secret), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token")
}
