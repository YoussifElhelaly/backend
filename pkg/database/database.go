package database

import (
	"fmt"
	"log"
	"os"
	"time"

	"whatify/backend/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Connect() {
	dsn := fmt.Sprintf(
		"host=%s user=%s dbname=%s port=%s sslmode=%s TimeZone=UTC",
		getEnv("DB_HOST", "localhost"),
		getEnv("DB_USER", "whatify"),
		getEnv("DB_NAME", "whatify"),
		getEnv("DB_PORT", "5432"),
		getEnv("DB_SSLMODE", "disable"),
	)
	if pw := getEnv("DB_PASSWORD", ""); pw != "" {
		dsn += " password=" + pw
	}

	// Use Silent in production to avoid leaking SQL into logs.
	logLevel := logger.Silent
	if os.Getenv("GIN_MODE") != "release" {
		logLevel = logger.Warn
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// Set DISABLE_AUTO_MIGRATE=true in production to skip GORM schema sync on
	// startup — run migrations manually with a dedicated migration tool instead.
	if os.Getenv("DISABLE_AUTO_MIGRATE") != "true" {
		// PayPal → PayTabs migration: rename old columns instead of leaving them
		// orphaned (GORM's AutoMigrate only adds new columns, it never renames).
		renameColumnIfExists(db, &models.Tenant{}, "paypal_sub_id", "paytabs_token")
		renameColumnIfExists(db, &models.Subscription{}, "paypal_sub_id", "paytabs_tran_ref")
		renameColumnIfExists(db, &models.PlanDef{}, "price_usd", "price_egp")
		renameColumnIfExists(db, &models.PlanDef{}, "original_price_usd", "original_price_egp")

		if err := db.AutoMigrate(
			&models.Tenant{},
			&models.User{},
			&models.WhatsAppSession{},
			&models.Contact{},
			&models.Tag{},
			&models.Conversation{},
			&models.Message{},
			&models.Campaign{},
			&models.CampaignContact{},
			&models.Funnel{},
			&models.FunnelStep{},
			&models.FunnelContact{},
			&models.FunnelContactHistory{},
			&models.Flow{},
			&models.FlowRun{},
			&models.ActivityLog{},
			&models.Product{},
			&models.QuickReply{},
			&models.APIKey{},
			&models.Webhook{},
			&models.Subscription{},
			&models.PlatformSetting{},
			&models.PlanDef{},
			&models.PasswordResetToken{},
			&models.EmailVerificationToken{},
			&models.RefreshToken{},
			&models.AIConfig{},
			&models.Lead{},
		); err != nil {
			log.Fatalf("failed to run migrations: %v", err)
		}
		log.Println("database: auto-migrate complete")

		// Replace the regular unique index on users.email with a partial one so
		// that soft-deleted users don't block re-registration with the same email.
		db.Exec(`DROP INDEX IF EXISTS idx_users_email`)
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_active ON users (email) WHERE deleted_at IS NULL`)

		// Unique contact per tenant (excluding soft-deleted).
		db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_contacts_tenant_phone ON contacts (tenant_id, phone_number) WHERE deleted_at IS NULL`)
	} else {
		log.Println("database: auto-migrate skipped (DISABLE_AUTO_MIGRATE=true)")
	}

	// Connection pool — prevents exhaustion under load.
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("failed to get underlying sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetMaxIdleConns(25)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	sqlDB.SetConnMaxIdleTime(2 * time.Minute)

	DB = db
	log.Println("database connected")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// renameColumnIfExists renames oldName to newName on the table backing model,
// but only when the table already exists, the old column is present, and the
// new one isn't (idempotent — safe to call on every startup).
func renameColumnIfExists(db *gorm.DB, model interface{}, oldName, newName string) {
	m := db.Migrator()
	if !m.HasTable(model) || !m.HasColumn(model, oldName) || m.HasColumn(model, newName) {
		return
	}
	if err := m.RenameColumn(model, oldName, newName); err != nil {
		log.Printf("database: failed to rename column %s -> %s: %v", oldName, newName, err)
	} else {
		log.Printf("database: renamed column %s -> %s", oldName, newName)
	}
}
