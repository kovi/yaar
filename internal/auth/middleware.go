package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/models"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Identify simply populates the context with user info if a valid token is found.
// It allows anonymous requests. It only fails if a token is present but invalid.
func Identify(secret string, db *gorm.DB, cache *UserCache) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Check for API Token
		if apiToken := c.GetHeader("X-API-Token"); apiToken != "" {
			hash := HashToken(apiToken)
			var t models.Token

			result := db.Preload("User").Where("secret_hash = ?", hash).Limit(1).Find(&t)

			if result.Error != nil {
				c.AbortWithStatusJSON(500, gin.H{"error": "Database error during authentication"})
				return
			}

			if t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API Token has expired"})
				return
			}

			if result.RowsAffected > 0 {
				// Success: Set context
				c.Set("user_id", t.UserID)
				c.Set("username", t.User.Username)
				c.Set("is_admin", t.User.IsAdmin)
				c.Set("allowed_paths", strings.Split(t.PathScope, ","))

				// UPDATE LAST USED:
				// We use a separate Update call to keep it efficient.
				// This won't trigger hooks or update 'updated_at' if you use .UpdateColumn
				db.Model(&t).UpdateColumn("last_used_at", time.Now())

				c.Next()
				return
			}

			// If token was provided but not found, reject the request
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API Token"})
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next() // Anonymous user
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid format"})
			return
		}

		claims, err := ValidateToken(parts[1], secret)
		if err != nil {
			logrus.Infof("validatetoken error: %v", err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Expired or invalid session"})
			return
		}

		exists, _, found := cache.Get(claims.UserID)
		if !found {
			// Cache miss: Check the real database
			var user models.User
			res := db.Select("id", "is_admin").Limit(1).Find(&user, claims.UserID)
			if res.Error != nil {
				logrus.Infof("DB query error: %v", res.Error)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Invalid auth"})
			}
			if res.RowsAffected == 0 {
				// User was likely deleted from DB
				cache.Set(claims.UserID, false, false, 5*time.Minute)
				c.AbortWithStatusJSON(401, gin.H{"error": "User no longer exists"})
				return
			}

			// Update local info
			exists = true
			claims.IsAdmin = user.IsAdmin
			cache.Set(claims.UserID, true, claims.IsAdmin, 2*time.Minute)
		}

		if !exists {
			c.AbortWithStatusJSON(401, gin.H{"error": "Account disabled"})
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("is_admin", claims.IsAdmin)
		c.Set("allowed_paths", strings.Split("/", "")) // Humans have full access by default in this design
		c.Next()
	}
}

// --- Logic Helpers (Directly usable in SmartRouter) ---

// EnsureAuth returns true if the user is identified, otherwise aborts with 401.
func EnsureAuth(c *gin.Context) bool {
	if _, exists := c.Get("username"); !exists {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return false
	}
	return true
}

// EnsureAdmin returns true if the user is an admin, otherwise aborts with 403.
// It automatically calls EnsureAuth first.
func EnsureAdmin(c *gin.Context) bool {
	if !EnsureAuth(c) {
		return false
	}
	isAdmin, _ := c.Get("is_admin")
	if isAdmin != true {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Admin privileges required"})
		return false
	}
	return true
}

// --- Middleware Wrappers (For r.Group use) ---

func Protect() gin.HandlerFunc {
	return func(c *gin.Context) {
		if EnsureAuth(c) {
			c.Next()
		}
	}
}

func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if EnsureAdmin(c) {
			c.Next()
		}
	}
}
