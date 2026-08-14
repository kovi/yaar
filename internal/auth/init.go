package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/internal/models"
	"gorm.io/gorm"
)

// GenerateInitialPassword returns a random password for the bootstrap admin.
//
// 18 bytes of crypto/rand rendered base64url gives 144 bits of entropy in 24
// characters that survive copy/paste out of a log line.
func GenerateInitialPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// bootstrapAdmin creates the initial admin account on a brand-new instance.
//
// It runs only when the user table is completely empty, so an existing
// installation never reaches this code and no deployed password is ever
// changed. New instances get a random password, printed once here — it is not
// recoverable afterwards, since only the bcrypt hash is stored. The previous
// hardcoded "admin123" shipped every fresh install with the same well-known
// credential.
func bootstrapAdmin(db *gorm.DB) error {
	var count int64
	if err := db.Model(&models.User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		// Existing installation: leave every account exactly as it is.
		return nil
	}

	password, err := GenerateInitialPassword()
	if err != nil {
		return fmt.Errorf("generate initial admin password: %w", err)
	}

	admin := models.User{Username: "admin", IsAdmin: true, AllowedPaths: models.StringList{"/"}}
	if err := admin.SetPassword(password); err != nil {
		return fmt.Errorf("hash initial admin password: %w", err)
	}
	if err := db.Create(&admin).Error; err != nil {
		return fmt.Errorf("create initial admin user: %w", err)
	}

	// Printed to stdout rather than the audit log or the logger on purpose:
	// this must not end up in a shipped log aggregator.
	fmt.Print("\n" + strings.Repeat("=", 72) + "\n" +
		"  Created initial admin user.\n\n" +
		"      username: admin\n" +
		"      password: " + password + "\n\n" +
		"  This password is shown ONCE and cannot be recovered.\n" +
		"  Store it now, then change it from the UI or via\n" +
		"  PATCH /_/api/admin/users/:id.\n" +
		strings.Repeat("=", 72) + "\n\n")

	return nil
}

func (h *AuthHandler) RegisterRoutes(r *gin.Engine, db *gorm.DB, cfg *config.Config, auditor *audit.Auditor) {
	// A failed bootstrap leaves an instance nobody can log into, so surface it
	// loudly rather than starting up in that state silently.
	if err := bootstrapAdmin(db); err != nil {
		h.Log.WithError(err).Error("failed to bootstrap the initial admin user")
	}

	r.POST("/_/api/login", h.Login)
	r.GET("/_/api/auth/me", Protect(), h.GetMe)
	admin := r.Group("/_/api/admin", AdminRequired())
	{
		admin.GET("/users", h.ListUsers)
		admin.POST("/users", h.CreateUser)
		admin.PATCH("/users/:id", h.UpdateUser)
		admin.DELETE("/users/:id", h.DeleteUser)
	}

	authorized := r.Group("/_/api", Protect())
	{
		authorized.GET("/tokens", h.ListTokens)
		authorized.POST("/tokens", h.CreateToken)
		authorized.DELETE("/tokens/:id", h.DeleteToken)
	}

}
