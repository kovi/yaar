package models

import "time"

type SystemState struct {
	ID        uint      `gorm:"primaryKey" json:"-"`
	Key       string    `gorm:"uniqueIndex" json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}
