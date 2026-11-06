// Package database initializes the GORM connection and runs migrations/seed.
package database

import (
	"fmt"
	"log"
	"time"

	"linux-ops-platform/backend/internal/config"
	"linux-ops-platform/backend/internal/models"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open connects to MySQL, configures the pool and optionally migrates.
func Open(cfg *config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.DSN()), &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Warn),
		DisableForeignKeyConstraintWhenMigrating: false,
	})
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(cfg.MySQL.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MySQL.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	if cfg.MySQL.AutoMigrate {
		if err := db.AutoMigrate(models.AllModels()...); err != nil {
			return nil, fmt.Errorf("auto migrate: %w", err)
		}
		log.Println("[db] auto-migration complete")
	}
	if cfg.MySQL.Seed {
		if err := Seed(db); err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
	}
	return db, nil
}

// Seed inserts demo groups/servers only when the tables are empty.
func Seed(db *gorm.DB) error {
	var count int64
	if err := db.Model(&models.Server{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if err := db.Create(&models.ServerGroup{Name: "default", Description: "default group"}).Error; err != nil {
		return err
	}
	var group models.ServerGroup
	if err := db.Where("name = ?", "default").First(&group).Error; err != nil {
		return err
	}
	tag := models.ServerTag{Name: "local"}
	if err := db.Create(&tag).Error; err != nil {
		return err
	}
	srv := models.Server{
		Name: "local-agent", IP: "127.0.0.1", AgentPort: 19090,
		GroupID: &group.ID, Status: models.StatusUnknownAgent,
	}
	if err := db.Create(&srv).Error; err != nil {
		return err
	}
	if err := db.Model(&srv).Association("Tags").Append(&tag); err != nil {
		return err
	}
	log.Println("[db] seed data inserted (local-agent -> 127.0.0.1:19090)")
	return nil
}
