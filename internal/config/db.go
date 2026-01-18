package config

import (
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func ConnectDB(dbFile string) (*gorm.DB, error) {
	// _busy_timeout=5000 (wait 5s)
	// _journal_mode=WAL (allow concurrent reads/writes)
	dsn := dbFile + "?_busy_timeout=5000&_journal_mode=WAL"

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
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

	return db, nil
}
