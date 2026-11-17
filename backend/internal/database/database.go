// Package database initializes the GORM connection and runs migrations/seed.
package database

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"linux-ops-platform/backend/internal/config"
	"linux-ops-platform/backend/internal/models"

	"golang.org/x/crypto/bcrypt"
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
		if err := Seed(db, cfg.Auth.AccessPassword); err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
	}
	if err := MigrateOwnership(db); err != nil {
		return nil, fmt.Errorf("migrate ownership: %w", err)
	}
	return db, nil
}

// Seed creates the bootstrap admin account and, only on a fresh install, the
// demo group/tag/server. Existing data is never overwritten.
//
// initialPassword is auth.access_password when configured; otherwise the
// documented default "changeme" is used. Either way the admin should change
// it after the first login.
func Seed(db *gorm.DB, initialPassword string) error {
	var admin models.User
	err := db.Where("role = ?", models.RoleAdmin).Order("id asc").First(&admin).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var byName models.User
		if err := db.Where("username = ?", "admin").First(&byName).Error; err == nil {
			// A legacy/pre-created "admin" row: promote it instead of
			// creating a duplicate.
			if err := db.Model(&byName).Update("role", models.RoleAdmin).Error; err != nil {
				return err
			}
			admin = byName
		} else {
			pw := initialPassword
			if pw == "" {
				pw = "changeme"
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			admin = models.User{
				Username: "admin", PasswordHash: string(hash),
				Role: models.RoleAdmin, CreatedBy: "system",
			}
			if err := db.Create(&admin).Error; err != nil {
				return err
			}
			log.Println("[db] seeded admin user; change the default password after login")
		}
	} else if err != nil {
		return err
	}

	// Assign legacy rows (created before accounts existed) to the admin so
	// nothing becomes invisible after the upgrade.
	db.Model(&models.Server{}).Where("owner_id = 0 OR owner_id IS NULL").
		Updates(map[string]interface{}{"owner_id": admin.ID, "owner_name": admin.Username})
	db.Model(&models.ExecutionTask{}).Where("owner_id = 0 OR owner_id IS NULL").
		Updates(map[string]interface{}{"owner_id": admin.ID, "owner_name": admin.Username})

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
		OwnerID: admin.ID, OwnerName: admin.Username,
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

// EnsureJWTSecret returns the key used to sign API JWTs. A configured secret
// wins; otherwise the key is read from app_settings and generated once on the
// first start, so issued tokens survive backend restarts. The fallback (when
// the setting cannot be persisted) is an ephemeral key: tokens issued with it
// stop working after a restart.
func EnsureJWTSecret(db *gorm.DB, configured string) string {
	if s := strings.TrimSpace(configured); s != "" {
		return s
	}
	var row models.Setting
	err := db.Where("name = ?", "jwt_secret").First(&row).Error
	if err == nil && row.Value != "" {
		return row.Value
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Printf("[auth] cannot generate JWT secret: %v", err)
		return ""
	}
	secret := hex.EncodeToString(b)
	rec := models.Setting{Name: "jwt_secret", Value: secret, UpdatedAt: time.Now().UTC()}
	if err := db.Create(&rec).Error; err != nil {
		log.Printf("[auth] cannot persist JWT secret (%v); using an ephemeral key", err)
		// Maybe another backend inserted it first; reuse that key.
		if err := db.Where("name = ?", "jwt_secret").First(&row).Error; err == nil && row.Value != "" {
			return row.Value
		}
		return secret
	}
	log.Println("[auth] generated JWT signing secret (stored in app_settings)")
	return secret
}

// MigrateOwnership backfills owner columns for databases created before the
// multi-user release. It is idempotent and safe to run on every start.
func MigrateOwnership(db *gorm.DB) error {
	var admin models.User
	if err := db.Where("role = ?", models.RoleAdmin).Order("id asc").First(&admin).Error; err != nil {
		return nil // no accounts yet; nothing to backfill
	}
	if err := db.Model(&models.Server{}).Where("owner_id = 0 OR owner_id IS NULL").
		Updates(map[string]interface{}{"owner_id": admin.ID, "owner_name": admin.Username}).Error; err != nil {
		return err
	}
	return db.Model(&models.ExecutionTask{}).Where("owner_id = 0 OR owner_id IS NULL").
		Updates(map[string]interface{}{"owner_id": admin.ID, "owner_name": admin.Username}).Error
}
