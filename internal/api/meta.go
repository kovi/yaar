package api

import (
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/config"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Handler struct {
	BaseDir string
	DB      *gorm.DB
	Config  *config.Config
	Log     *logrus.Entry
	Audit   *audit.Auditor
}

func (h *Handler) GetFileMeta(path string) (*MetaResource, error) {
	var res MetaResource
	r := h.DB.Preload("Tags").
		Where("path = ?", path).
		Limit(1).Find(&res)

	if r.Error != nil {
		return nil, r.Error
	}

	if r.RowsAffected != 1 {
		return nil, nil
	}

	return &res, nil
}
