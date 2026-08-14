package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/internal/models"
	"github.com/kovi/yaar/internal/utils"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type AuthHandler struct {
	DB        *gorm.DB
	Config    config.Config
	Audit     *audit.Auditor
	UserCache UserCache
	Log       *logrus.Entry
}

// log returns the request-scoped logger when one is available, falling back to
// the handler's own logger. See the equivalent on api.Handler for the rationale.
func (h *AuthHandler) log(c *gin.Context) *logrus.Entry {
	if c != nil {
		if v, ok := c.Get("logger"); ok {
			if e, ok := v.(*logrus.Entry); ok {
				return e
			}
		}
	}
	return h.Log
}

func bindJSONStrict(c *gin.Context, obj any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(obj)
}

// Login handles POST /_/api/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}

	if err := bindJSONStrict(c, &req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Throttle per (username, client IP) — see login_throttle.go for why the
	// pair, and not the username alone, is the key.
	throttleKey := req.Username + "\x00" + c.ClientIP()

	if wait := loginThrottler.retryAfter(throttleKey); wait > 0 {
		seconds := int(wait.Seconds()) + 1
		c.Header("Retry-After", strconv.Itoa(seconds))
		h.Audit.WithContext(c).Failure(audit.ActionLogin, req.Username,
			errors.New("locked out"), "reason", "too_many_failed_attempts")
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": fmt.Sprintf("Too many failed login attempts. Try again in %d seconds.", seconds),
		})
		return
	}

	var user models.User
	err := h.DB.Where("username = ?", req.Username).Limit(1).Find(&user).Error
	found := err == nil && user.ID != 0

	// Always run a password comparison, even when the user does not exist.
	// Returning early on a miss makes an unknown username measurably faster
	// than a known one with a wrong password, which reveals which accounts
	// exist. CheckPassword on the zero-value user compares against an empty
	// hash, so this stays a constant-ish cost without ever authenticating.
	passwordOK := user.CheckPassword(req.Password)

	if err != nil || !found || !passwordOK {
		lockout := loginThrottler.recordFailure(throttleKey)

		reason := "bad_password"
		if !found {
			reason = "unknown_user"
		}
		kv := []any{"reason", reason}
		if lockout > 0 {
			kv = append(kv, "locked_out_for", lockout.String())
		}
		h.Audit.WithContext(c).Failure(audit.ActionLogin, req.Username,
			errors.New("invalid credentials"), kv...)

		// The response stays identical in all three cases so it leaks nothing
		// about which usernames exist.
		c.JSON(401, gin.H{"error": "Invalid credentials"})
		return
	}

	loginThrottler.recordSuccess(throttleKey)

	// 1. Generate the token using the secret from your config
	token, err := GenerateToken(user, h.Config.Server.JwtSecret)
	if err != nil {
		c.JSON(500, gin.H{"error": "Could not generate token"})
		return
	}

	h.Audit.WithContext(c).Success(audit.ActionLogin, req.Username)

	// 2. Return token + basic user info for the UI
	c.JSON(200, gin.H{
		"token":         token,
		"username":      user.Username,
		"is_admin":      user.IsAdmin,
		"allowed_paths": user.AllowedPaths,
	})
}

// ListUsers handles GET /_/api/admin/users
func (h *AuthHandler) ListUsers(c *gin.Context) {
	var users []models.User
	h.DB.Select("id", "username", "is_admin", "created_at", "allowed_paths").Find(&users)
	c.JSON(200, users)
}

// CreateUser handles POST /_/api/admin/users
func (h *AuthHandler) CreateUser(c *gin.Context) {
	var req struct {
		Username     string            `json:"username" binding:"required"`
		Password     string            `json:"password" binding:"required"`
		AllowedPaths models.StringList `json:"allowed_paths"`
		IsAdmin      bool              `json:"is_admin"`
	}
	if err := bindJSONStrict(c, &req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var existingUser models.User
	if err := h.DB.Where("username = ?", req.Username).First(&existingUser).Error; err == nil {
		c.JSON(400, gin.H{"error": "User with this username already exists"})
		return
	}

	user := models.User{Username: req.Username, IsAdmin: req.IsAdmin, AllowedPaths: req.AllowedPaths}
	user.SetPassword(req.Password)

	if err := h.DB.Create(&user).Error; err != nil {
		h.log(c).WithError(err).Error("User create failed")
		h.Audit.WithContext(c).Failure(
			audit.ActionUserCreate,
			req.Username,
			err,
			"created_by", c.GetString("username"),
			"is_admin", req.IsAdmin,
		)
		c.JSON(500, gin.H{"error": "Could not create user: " + err.Error()})
		return
	}

	// is_admin and allowed_paths are recorded because they are the privilege
	// grant: "who was given admin, by whom" is the question this entry answers.
	h.Audit.WithContext(c).Success(
		audit.ActionUserCreate,
		user.Username,
		"created_by", c.GetString("username"),
		"is_admin", user.IsAdmin,
		"allowed_paths", user.AllowedPaths,
	)

	c.JSON(201, user)
}

func (h *AuthHandler) GetMe(c *gin.Context) {
	username, _ := c.Get("username")
	isAdmin, _ := c.Get("is_admin")
	userId, _ := c.Get("user_id")

	c.JSON(200, gin.H{
		"id":       userId,
		"username": username,
		"is_admin": isAdmin,
	})
}

// UpdateUser handles PATCH /_/api/admin/users/:id
func (h *AuthHandler) UpdateUser(c *gin.Context) {
	id := c.Param("id")
	currentUserID := c.MustGet("user_id").(uint)

	var req struct {
		Password     *string           `json:"password"`
		IsAdmin      *bool             `json:"is_admin"`
		AllowedPaths models.StringList `json:"allowed_paths"`
	}

	if err := bindJSONStrict(c, &req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	var user models.User
	if err := h.DB.First(&user, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "User not found"})
		return
	}

	// Safety: Prevent self-demotion
	if fmt.Sprint(currentUserID) == id && req.IsAdmin != nil && !*req.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot remove your own admin status"})
		return
	}

	// Update logic
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if req.Password != nil && *req.Password != "" {
			if err := user.SetPassword(*req.Password); err != nil {
				return err
			}
		}

		updates := make(map[string]any)
		if req.IsAdmin != nil {
			updates["is_admin"] = *req.IsAdmin
		}
		if req.Password != nil {
			updates["password_hash"] = user.PasswordHash
		}
		if req.AllowedPaths != nil {
			updates["allowed_paths"] = req.AllowedPaths
		}
		return tx.Model(&user).Updates(updates).Error
	})

	if err != nil {
		h.Audit.WithContext(c).Failure(
			"USER_UPDATE",
			user.Username,
			err,
			"changed_by", c.GetString("username"),
			"is_admin_set", req.IsAdmin != nil,
		)

		c.JSON(500, gin.H{"error": "Update failed"})
		return
	}

	h.UserCache.Invalidate(user.ID)

	h.Audit.WithContext(c).Success(
		"USER_UPDATE",
		user.Username,
		"changed_by", c.GetString("username"),
		"is_admin_set", req.IsAdmin != nil,
	)

	c.JSON(200, gin.H{"status": "updated", "username": user.Username})
}

// DeleteUser handles DELETE /_/api/admin/users/:id
func (h *AuthHandler) DeleteUser(c *gin.Context) {
	id := c.Param("id")
	currentUserID := c.MustGet("user_id").(uint)

	// Safety: Prevent self-deletion
	h.log(c).Infof("id: %v %v", fmt.Sprint(currentUserID), id)
	if fmt.Sprint(currentUserID) == id {
		c.JSON(http.StatusForbidden, gin.H{"error": "You cannot delete your own account"})
		return
	}

	var user models.User
	if err := h.DB.First(&user, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "User not found"})
		return
	}

	if err := h.DB.Delete(&user).Error; err != nil {
		h.Audit.WithContext(c).Failure(
			"USER_DELETE",
			user.Username,
			err,
			"deleted_by", c.GetString("username"),
		)
		c.JSON(500, gin.H{"error": "Failed to delete user"})
		return
	}

	h.UserCache.Invalidate(user.ID)

	h.Audit.WithContext(c).Success(
		"USER_DELETE",
		user.Username,
		"deleted_by", c.GetString("username"),
	)

	c.Status(http.StatusNoContent)
}

// CreateToken handles POST /_/api/tokens
func (h *AuthHandler) CreateToken(c *gin.Context) {
	currentUserID := c.MustGet("user_id").(uint)
	isAdmin := c.GetBool("is_admin")

	var req struct {
		UserID       uint     `json:"user_id"`
		Name         string   `json:"name" binding:"required"`
		AllowedPaths []string `json:"allowed_paths"`
		Expires      string   `json:"expires"`
	}

	if err := bindJSONStrict(c, &req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.Name == "" {
		c.JSON(400, gin.H{"error": "name is required"})
		return
	}

	// Logic: If user is not admin, they can ONLY create tokens for themselves.
	targetUserID := currentUserID
	if isAdmin && req.UserID != 0 {
		targetUserID = req.UserID
	}

	var expiresAt *time.Time
	if req.Expires != "" {
		t, err := utils.ParseExpiry(req.Expires)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid expiry format"})
			return
		}
		expiresAt = &t
	}

	plainToken, _ := GenerateRandomToken()
	token := models.Token{
		UserID:       targetUserID,
		Name:         req.Name,
		AllowedPaths: req.AllowedPaths,
		ExpiresAt:    expiresAt,
		SecretHash:   HashToken(plainToken),
	}

	if err := h.DB.Create(&token).Error; err != nil {
		h.log(c).WithError(err).Error("Failed to create token")
		c.JSON(500, gin.H{"error": "Failed to create token"})
		return
	}

	// The owner is looked up rather than read off token.User: the Token above is
	// constructed literally with only UserID set, so the association is the zero
	// value and this field logged an empty owner on every token ever issued.
	ownerName := c.GetString("username")
	if targetUserID != currentUserID {
		var owner models.User
		if err := h.DB.Select("username").First(&owner, targetUserID).Error; err == nil {
			ownerName = owner.Username
		}
	}

	h.Audit.WithContext(c).Success(
		audit.ActionTokenCreate,
		token.Name,
		"owner", ownerName,
		"allowed_paths", token.AllowedPaths,
	)

	// IMPORTANT: We return the plainToken ONLY ONCE here.
	c.JSON(201, gin.H{
		"id":            token.ID,
		"plain_token":   plainToken,
		"name":          token.Name,
		"allowed_paths": token.AllowedPaths,
	})
}

// ListTokens handles GET /_/api/tokens
func (h *AuthHandler) ListTokens(c *gin.Context) {
	currentUserID := c.MustGet("user_id").(uint)
	isAdmin := c.GetBool("is_admin")

	query := h.DB.Preload("User")

	// If not admin, or if admin didn't explicitly ask for "all"
	// we only show the user's own tokens.
	if !isAdmin {
		query = query.Where("user_id = ?", currentUserID)
	}

	var tokens []models.Token
	if err := query.Find(&tokens).Error; err != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	c.JSON(200, tokens)
}

// DeleteToken handles DELETE /_/api/tokens/:id
func (h *AuthHandler) DeleteToken(c *gin.Context) {
	id := c.Param("id")
	currentUserID := c.MustGet("user_id").(uint)
	isAdmin := c.GetBool("is_admin")

	// User is preloaded so the audit entry names the token's owner, which is the
	// point of the record when an admin revokes someone else's credential.
	var token models.Token
	if err := h.DB.Preload("User").First(&token, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "Token not found"})
		return
	}

	// OWNERSHIP CHECK: Only delete if owner OR admin
	if token.UserID != currentUserID && !isAdmin {
		h.Audit.WithContext(c).Failure(
			audit.ActionTokenDelete,
			token.Name,
			errors.New("not owner and not admin"),
			"owner", token.User.Username,
		)
		c.JSON(403, gin.H{"error": "You do not have permission to revoke this token"})
		return
	}
	h.log(c).Infof("token id to delete: tokenID:%v id:%v", token.ID, id)
	if err := h.DB.Delete(&token).Error; err != nil {
		h.log(c).WithError(err).Error("failed to delete token")
		h.Audit.WithContext(c).Failure(
			audit.ActionTokenDelete,
			token.Name,
			err,
			"owner", token.User.Username,
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke token"})
		return
	}

	h.Audit.WithContext(c).Success(
		audit.ActionTokenDelete,
		token.Name,
		"owner", token.User.Username,
		"revoked_by", c.GetString("username"),
	)

	c.Status(204)
}
