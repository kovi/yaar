package models

import (
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Token struct {
	ID           uint       `gorm:"primaryKey"`
	UserID       uint       `gorm:"index"`
	User         User       `gorm:"constraint:OnDelete:CASCADE"`
	Name         string     `gorm:"not null"`                       // Name for the CI/CD job (e.g. "Jenkins-App-A")
	SecretHash   string     `gorm:"uniqueIndex"`                    // The hashed token
	AllowedPaths StringList `gorm:"type:text" json:"allowed_paths"` // Stores as ["/a", "/b"]
	LastUsedAt   *time.Time `json:"last_used_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	CreatedAt    time.Time
}

type User struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	Username     string     `gorm:"uniqueIndex;not null" json:"username"`
	PasswordHash string     `gorm:"not null" json:"-"`
	IsAdmin      bool       `gorm:"default:false" json:"is_admin"`
	AllowedPaths StringList `gorm:"type:text;default:'[]'" json:"allowed_paths"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (u *User) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(hash)
	return nil
}

func (u *User) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password))
	return err == nil
}
