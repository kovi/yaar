package config

import (
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func ConnectDB(dbFile string) (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(dbFile), &gorm.Config{
		PrepareStmt: true,
		// This tells GORM to convert all time.Time objects to UTC before saving
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})

	if err != nil {
		return nil, err
	}

	// 2. Tweak the connection pool
	sqlDB, _ := db.DB()

	// For SQLite, it is often best to limit open connections to 1
	// if you want to be 100% safe from locking, but with WAL/Timeout,
	// you can usually leave it default or set to a low number.
	sqlDB.SetMaxOpenConns(1)

	// Apply pragmas
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA foreign_keys = ON;",
	}

	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			return nil, fmt.Errorf("failed to apply %s: %w", p, err)
		}
	}

	var mode string
	sqlDB.QueryRow("PRAGMA journal_mode;").Scan(&mode)
	logrus.Infof("db journal_mode: %v", mode)

	return db, nil
}
