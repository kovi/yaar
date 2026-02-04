package auth

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/internal/models"
	"gorm.io/gorm"
)

func bootstrapAdmin(db *gorm.DB) {
	var count int64
	db.Model(&models.User{}).Count(&count)
	if count == 0 {
		admin := models.User{Username: "admin", IsAdmin: true, AllowedPaths: models.StringList{"/"}}
		admin.SetPassword("admin123")
		db.Create(&admin)
		fmt.Println("Created default admin user: admin / admin123")
	}
}

func (h *AuthHandler) RegisterRoutes(r *gin.Engine, db *gorm.DB, cfg *config.Config, auditor *audit.Auditor) {
	bootstrapAdmin(db)

	r.POST("/_/api/login", h.Login)
	r.GET("/_/api/auth/me", Protect(), h.GetMe)
	admin := r.Group("/_/api/admin", AdminRequired())
	{
		admin.GET("/users", h.ListUsers)
		admin.POST("/users", h.CreateUser)
		admin.PATCH("/users/:id", h.UpdateUser)
		admin.DELETE("/users/:id", h.DeleteUser)

		admin.GET("/tokens", h.ListTokens)
		admin.POST("/tokens", h.CreateToken)
		admin.DELETE("/tokens/:name", h.DeleteToken)
	}

}
