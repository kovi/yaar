package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/models"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// auditDenial records an authorization failure when an auditor is available on
// the context. Denials are stored per-request by SetAuditor rather than in a
// package variable so that tests running against separate app instances do not
// share one.
const auditorKey = "auditor"

// SetAuditor makes the auditor available to the authorization gates, which are
// package-level middlewares and so cannot capture it at construction.
func SetAuditor(a *audit.Auditor) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(auditorKey, a)
		c.Next()
	}
}

// reqLogger returns the request-scoped logger, falling back to the standard one
// when the logging middleware is not installed (as in narrow unit tests).
func reqLogger(c *gin.Context) *logrus.Entry {
	if v, ok := c.Get("logger"); ok {
		if e, ok := v.(*logrus.Entry); ok {
			return e
		}
	}
	return logrus.NewEntry(logrus.StandardLogger())
}

// enrichLogger folds the resolved identity into the request-scoped logger set by
// the logging middleware.
//
// That middleware builds its entry before authentication has run, so it can only
// know request_id/method/path. Re-setting the entry here means every handler line
// logged through Handler.log carries the caller too, without each handler having
// to add the fields itself.
func enrichLogger(c *gin.Context, fields logrus.Fields) {
	v, ok := c.Get("logger")
	if !ok {
		return
	}
	e, ok := v.(*logrus.Entry)
	if !ok {
		return
	}
	c.Set("logger", e.WithFields(fields))
}

func auditDenial(c *gin.Context, resource, reason string) {
	v, ok := c.Get(auditorKey)
	if !ok {
		return
	}
	a, ok := v.(*audit.Auditor)
	if !ok || a == nil {
		return
	}
	a.WithContext(c).Failure(audit.ActionAuthDenied, resource, errors.New(reason))
}

// Identify simply populates the context with user info if a valid token is found.
// It allows anonymous requests. It only fails if a token is present but invalid.
func Identify(secret string, db *gorm.DB, cache *UserCache) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		// Both credential kinds travel in `Authorization: Bearer`. API tokens are
		// opaque and carry the af_ prefix from GenerateRandomToken; anything else
		// is treated as a JWT. The alphabets are disjoint (a JWT's base64url
		// segments cannot start with "af_" and decode to a valid header), so the
		// prefix is an unambiguous discriminator.
		if strings.HasPrefix(parts[1], APITokenPrefix) {
			hash := HashToken(parts[1])
			var t models.Token

			result := db.Preload("User").Where("secret_hash = ?", hash).Limit(1).Find(&t)

			if result.Error != nil {
				c.AbortWithStatusJSON(500, gin.H{"error": "Database error during authentication"})
				return
			}

			// Check expiry only once we know the token exists; on a miss `t` is the
			// zero value and ExpiresAt is nil.
			if result.RowsAffected == 0 {
				auditDenial(c, c.Request.URL.Path, "unknown api token")
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API Token"})
				return
			}

			if t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt) {
				auditDenial(c, c.Request.URL.Path, "expired api token")
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API Token has expired"})
				return
			}

			// Success: Set context
			c.Set("user_id", t.UserID)
			c.Set("username", t.User.Username)
			c.Set("is_admin", t.User.IsAdmin)
			c.Set("allowed_paths", []string(t.AllowedPaths))
			c.Set("token_name", t.Name)
			c.Set("token_id", t.ID)

			enrichLogger(c, logrus.Fields{
				"user":       t.User.Username,
				"token_name": t.Name,
			})

			// UPDATE LAST USED:
			// Coarsened to lastUsedResolution. This is a synchronous write
			// against a MaxOpenConns(1) SQLite handle, so doing it per request
			// serialized every authenticated call behind a write lock — a real
			// contention point under CI load. last_used_at only needs to be
			// accurate enough to spot dormant tokens.
			// UpdateColumn avoids hooks and leaves 'updated_at' alone.
			if shouldRecordTokenUse(t.ID, t.LastUsedAt) {
				db.Model(&t).UpdateColumn("last_used_at", time.Now())
			}

			c.Next()
			return
		}

		claims, err := ValidateToken(parts[1], secret)
		if err != nil {
			reqLogger(c).WithError(err).Info("rejected session token")
			auditDenial(c, c.Request.URL.Path, "invalid or expired session")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Expired or invalid session"})
			return
		}

		exists, found, _ := cache.Get(claims.UserID)
		if !found {
			// Cache miss: Check the real database
			var user models.User
			res := db.Select("id", "is_admin", "allowed_paths").Limit(1).Find(&user, claims.UserID)
			if res.Error != nil {
				reqLogger(c).WithError(res.Error).Error("auth: user lookup failed")
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Invalid auth"})
				return
			}
			if res.RowsAffected == 0 {
				// User was likely deleted from DB
				cache.Set(claims.UserID, false, false, []string{}, 5*time.Minute)
				c.AbortWithStatusJSON(401, gin.H{"error": "User no longer exists"})
				return
			}

			// Update local info
			exists = true
			claims.IsAdmin = user.IsAdmin
			claims.AllowedPaths = user.AllowedPaths
			cache.Set(claims.UserID, true, claims.IsAdmin, claims.AllowedPaths, 2*time.Minute)
		}

		if !exists {
			c.AbortWithStatusJSON(401, gin.H{"error": "Account disabled"})
			return
		}
		enrichLogger(c, logrus.Fields{"user": claims.Username})

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("is_admin", claims.IsAdmin)
		c.Set("allowed_paths", []string(claims.AllowedPaths))
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
		// An identified non-admin probing an admin route is the signal worth
		// keeping: unlike an anonymous 401, it names a real account.
		auditDenial(c, c.Request.URL.Path, "admin privileges required")
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
