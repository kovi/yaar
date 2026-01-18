package models

import "time"

type Token struct {
	ID         uint       `gorm:"primaryKey"`
	UserID     uint       `gorm:"index"`
	User       User       `gorm:"constraint:OnDelete:CASCADE"`
	Name       string     `gorm:"not null"`    // Name for the CI/CD job (e.g. "Jenkins-App-A")
	SecretHash string     `gorm:"uniqueIndex"` // The hashed token
	PathScope  string     `gorm:"default:'/'"` // Restrict to this directory prefix
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time
}
