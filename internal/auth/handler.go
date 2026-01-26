package auth

import (
	"fmt"
	"net/http"
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

// Login handles POST /_/api/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request"})
		return
	}

	var user models.User
	if err := h.DB.Where("username = ?", req.Username).Limit(1).Find(&user).Error; err != nil {
		c.JSON(401, gin.H{"error": "Invalid credentials"})
		return
	}

	if !user.CheckPassword(req.Password) {
		c.JSON(401, gin.H{"error": "Invalid credentials"})
		return
	}

	// 1. Generate the token using the secret from your config
	token, err := GenerateToken(user, h.Config.Server.JwtSecret)
	if err != nil {
		c.JSON(500, gin.H{"error": "Could not generate token"})
		return
	}

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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	user := models.User{Username: req.Username, IsAdmin: req.IsAdmin, AllowedPaths: req.AllowedPaths}
	user.SetPassword(req.Password)

	if err := h.DB.Create(&user).Error; err != nil {
		c.JSON(500, gin.H{"error": "Could not create user"})
		return
	}
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

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request body"})
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
	logrus.Infof("id: %v %v", fmt.Sprint(currentUserID), id)
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

// CreateToken handles POST /_/api/admin/tokens
func (h *AuthHandler) CreateToken(c *gin.Context) {
	var req struct {
		UserID       uint     `json:"user_id" binding:"required"`
		Name         string   `json:"name" binding:"required"`
		AllowedPaths []string `json:"allowed_paths"`
		Expires      string   `json:"expires"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var expiresAt *time.Time
	if req.Expires != "" {
		t, err := utils.ParseExpiry(req.Expires) // Reusing our smart parser
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid expiry format"})
			return
		}
		expiresAt = &t
	}

	plainToken, _ := GenerateRandomToken()
	token := models.Token{
		UserID:       req.UserID,
		Name:         req.Name,
		AllowedPaths: req.AllowedPaths,
		ExpiresAt:    expiresAt,
		SecretHash:   HashToken(plainToken),
	}

	if err := h.DB.Create(&token).Error; err != nil {
		h.Log.WithError(err).Error("Failed to create token")
		c.JSON(500, gin.H{"error": "Failed to create token"})
		return
	}

	h.Audit.WithContext(c).Success(
		"TOKEN_CREATED",
		token.Name,
		"owner", token.User.Username,
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

// ListTokens handles GET /_/api/admin/tokens
func (h *AuthHandler) ListTokens(c *gin.Context) {
	var tokens []models.Token
	h.DB.Preload("User").Find(&tokens)
	c.JSON(200, tokens)
}

// DeleteToken handles DELETE /_/api/admin/tokens/:id
func (h *AuthHandler) DeleteToken(c *gin.Context) {
	tokenID := c.Param("id")

	// 1. Find the token first (to check if it exists and get data for auditing)
	var token models.Token
	if err := h.DB.Preload("User").First(&token, tokenID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Token not found"})
		return
	}

	// 2. Physical Deletion
	if err := h.DB.Delete(&token).Error; err != nil {
		h.Log.WithError(err).Error("Failed to delete API token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke token"})
		return
	}

	// 3. AUDIT: Record that an admin revoked a token
	h.Audit.WithContext(c).Success(
		"TOKEN_REVOKE",
		token.Name,
		"owner", token.User.Username,
		"allowed_paths", token.AllowedPaths,
	)

	// 4. Return 204 No Content
	c.Status(http.StatusNoContent)
}
