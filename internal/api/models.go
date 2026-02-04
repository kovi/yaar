package api

import (
	"time"

	"github.com/kovi/yaar/internal/models"
	"gorm.io/gorm"
)

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&MetaResource{},
		&MetaTag{},
		&models.User{},
		&models.Token{},
	)
}

type ResourcePolicy struct {
	IsImmutable bool `json:"is_immutable,omitempty"` // Direct flag on this specific record
	IsProtected bool `json:"is_protected,omitempty"` // Path-based (from YAML config)
	IsAllowed   bool `json:"is_allowed,omitempty"`   // Scope-based (for the current user)
}

type FileResponse struct {
	Name           string                   `json:"name"`
	IsDir          bool                     `json:"isdir"`
	Size           int64                    `json:"size"`
	ModTime        time.Time                `json:"modtime"`
	ExpiresAt      time.Time                `json:"expires_at,omitempty"`
	Tags           []MetaTag                `json:"tags,omitempty"`
	Stream         string                   `json:"stream,omitempty"`
	Group          string                   `json:"group,omitempty"`
	KeepLatest     bool                     `json:"keep_latest,omitempty"`
	ContentType    string                   `json:"contenttype,omitempty"`
	ChecksumSHA1   string                   `json:"checksum_sha1,omitempty"`
	ChecksumSHA256 string                   `json:"checksum_sha256,omitempty"`
	ChecksumMD5    string                   `json:"checksum_md5,omitempty"`
	Policy         ResourcePolicy           `json:"policy"`
	DownloadMode   models.BatchDownloadMode `json:"download_mode"`
}

type ResourceType string

const (
	ResourceTypeFile ResourceType = "file"
	ResourceTypeDir  ResourceType = "dir"
)

type MetaResource struct {
	ID          uint         `gorm:"primaryKey"`
	Path        string       `gorm:"type:text;not null;uniqueIndex"`
	Type        ResourceType `gorm:"type:text;not null;default:'file';index"`
	ContentType string       `gorm:"type:text"`
	Size        int64
	ModTime     time.Time `gorm:"index"`

	Stream *string `gorm:"type:text;"`
	Group  *string `gorm:"type:text;"`

	Tags             []MetaTag  `gorm:"foreignKey:ResourceID;constraint:OnDelete:CASCADE"`
	ExpiresAt        *time.Time `gorm:"index:idx_group_expires"`
	Immutable        *bool      `gorm:"default:false"`
	PolicyKeepLatest *bool      `gorm:"default:false"`

	MD5    string `gorm:"size:32;index"`
	SHA1   string `gorm:"size:40;index"`
	SHA256 string `gorm:"size:64;index"`

	DownloadMode models.BatchDownloadMode `gorm:"type:text;default:'literal'"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

type MetaTag struct {
	ID         uint          `gorm:"primaryKey"`
	ResourceID uint          `gorm:"index"`
	Resource   *MetaResource `gorm:"foreignKey:ResourceID"`
	Key        string        `gorm:"size:255;index" json:"key"`
	Value      string        `gorm:"size:255" json:"value"`
}

type MetaPatchRequest struct {
	ExpiresAt    *string                   `json:"expires_at"`
	Tags         *string                   `json:"tags"`
	Immutable    *bool                     `json:"immutable"`
	Stream       *string                   `json:"stream"`
	KeepLatest   *bool                     `json:"keep_latest"`
	ContentType  *string                   `json:"contenttype"`
	DownloadMode *models.BatchDownloadMode `json:"download_mode"`
}
