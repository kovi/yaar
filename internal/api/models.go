package api

import "time"

type ResourceType string

const (
	ResourceTypeFile ResourceType = "file"
	ResourceTypeDir  ResourceType = "dir"
)

type MetaTag struct {
	ID         uint          `gorm:"primaryKey"`
	ResourceID uint          `gorm:"index"`
	Resource   *MetaResource `gorm:"foreignKey:ResourceID"`
	Key        string        `gorm:"size:255;index" json:"key"`
	Value      string        `gorm:"size:255" json:"value"`
}

type Stream struct {
	ID                    uint   `gorm:"primaryKey"`
	Name                  string `gorm:"type:text;not null;uniqueIndex"`
	RetainLatest          *bool
	RetainLatestMaxExpiry *string `gorm:"type:text"` // "90d"
	AutoExpirePrevious    *bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type Group struct {
	ID                   uint           `gorm:"primaryKey"`
	StreamID             uint           `gorm:"not null;index"`
	Stream               Stream         `gorm:"foreignKey:StreamID"`
	Name                 string         `gorm:"type:text;not null"`
	EffectiveDeleteAfter *time.Time     `gorm:"index"`
	ExpiredReason        *string        `gorm:"type:text"`
	Members              []MetaResource `gorm:"foreignKey:GroupID"`
	CreatedAt            time.Time      `gorm:"index"`
	UpdatedAt            time.Time
}

type MetaResource struct {
	ID          uint         `gorm:"primaryKey"`
	Path        string       `gorm:"type:text;not null;uniqueIndex"`
	Type        ResourceType `gorm:"type:text;not null;default:'file';index"`
	ContentType string       `gorm:"type:text"`
	Size        int64
	ModTime     time.Time `gorm:"index"`

	// Group membership
	GroupID *uint  `gorm:"index"`
	Group   *Group `gorm:"foreignKey:GroupID"`

	// User-defined expiry policies (source of truth for recalculation)
	ExpiryAt            *time.Time
	ExpiryAfterUpload   *string `gorm:"type:text"` // "30d", "7d", etc.
	ExpiryAfterDownload *string `gorm:"type:text"` // sliding window, reset on each GET

	// Computed deadline — janitor checks this alongside group.delete_after
	DeleteAfter *time.Time `gorm:"index"`

	// Resource-level flags
	Immutable     *bool
	AutoPrune     *bool `gorm:"index"`
	PruneChildren *bool `gorm:"index"`

	// Checksums
	MD5    string `gorm:"size:32;index"`
	SHA1   string `gorm:"size:40;index"`
	SHA256 string `gorm:"size:64;index"`

	// Relations
	Tags []MetaTag `gorm:"foreignKey:ResourceID;constraint:OnDelete:CASCADE"`

	// Audit
	LastDownloadedAt *time.Time
	CreatedAt        time.Time `gorm:"index"`
	UpdatedAt        time.Time
}

// ResourceResponse is the UI-facing representation of a MetaResource
type ResourceResponse struct {
	ID          uint         `json:"id"`
	Path        string       `json:"path"`
	Name        string       `json:"name"` // basename of Path
	Type        ResourceType `json:"type"`
	ContentType string       `json:"content_type,omitempty"`
	Size        int64        `json:"size"`
	ModTime     time.Time    `json:"modTime"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`

	// Stream / group
	Stream *string `json:"stream,omitempty"`
	Group  *string `json:"group,omitempty"`

	Policy    ResourcePolicy     `json:"policy"`
	Retention *RetentionResponse `json:"retention,omitempty"`
	Checksums ChecksumResponse   `json:"checksums"`
	Tags      []TagResponse      `json:"tags,omitempty"`
}

// Access control policies (NOT stored in DB, computed per request)
type ResourcePolicy struct {
	IsImmutable bool `json:"is_immutable"` // From MetaResource.Immutable
	IsProtected bool `json:"is_protected"` // From config (path-based rules)
	IsWritable  bool `json:"is_writable"`  // Computed: can user modify?
	IsAllowed   bool `json:"is_allowed"`   // Computed: is path within user scopes?
}

// RetentionResponse shows configured retention policies
type RetentionResponse struct {
	Immutable     bool                  `json:"immutable,omitempty"`
	AutoPrune     bool                  `json:"auto_prune,omitempty"`
	PruneChildren bool                  `json:"prune_children,omitempty"`
	StreamGroups  *StreamGroupsResponse `json:"stream_groups,omitempty"`
	Expires       *ExpiresResponse      `json:"expires,omitempty"`
}

type StreamGroupsResponse struct {
	AutoExpirePrevious bool    `json:"auto_expire_previous,omitempty"`
	LatestExpiryPolicy string  `json:"latest_expiry_policy,omitempty"`
	LatestRetainUntil  *string `json:"latest_retain_until,omitempty"`
}

type ExpiresResponse struct {
	AfterUpload   string  `json:"after_upload,omitempty"`
	AfterDownload string  `json:"after_download,omitempty"`
	At            *string `json:"at,omitempty"`
	Effective     *string `json:"effective,omitempty"` // Resolved expiry time (group deadline takes precedence)
}

type ChecksumResponse struct {
	MD5    string `json:"md5,omitempty"`
	SHA1   string `json:"sha1,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type TagResponse struct {
	ID    uint   `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

type MetaPatchRequest struct {
	Retention   *RetentionPolicyPatch `json:"retention"`
	Tags        *string               `json:"tags"`
	Stream      *string               `json:"stream"`
	Group       *string               `json:"group"`
	ContentType *string               `json:"contenttype"`
}

type RetentionPolicyPatch struct {
	Immutable     *bool         `json:"immutable"`
	AutoPrune     *bool         `json:"auto_prune"`
	PruneChildren *bool         `json:"prune_children"`
	Expires       *ExpiresPatch `json:"expires"` // Replace entire object
}

type ExpiresPatch struct {
	AfterUpload   *string `json:"after_upload"`   // "30d"
	AfterDownload *string `json:"after_download"` // "7d"
	At            *string `json:"at"`             // "2027-04-12T00:00:00Z"
}
