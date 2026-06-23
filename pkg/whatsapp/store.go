package whatsapp

import (
	"context"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

var Container *sqlstore.Container

func InitStore() error {
	dsn := fmt.Sprintf(
		"host=%s user=%s dbname=%s port=%s sslmode=%s TimeZone=UTC",
		getEnv("DB_HOST", "localhost"),
		getEnv("DB_USER", "helaly"),
		getEnv("DB_NAME", "whatify"),
		getEnv("DB_PORT", "5432"),
		getEnv("DB_SSLMODE", "disable"),
	)
	if pw := getEnv("DB_PASSWORD", ""); pw != "" {
		dsn += " password=" + pw
	}

	// whatsmeow sqlstore expects a URL-style DSN for pgx.
	// Include password in userinfo if set (percent-encode not needed for typical passwords).
	userInfo := getEnv("DB_USER", "helaly")
	if pw := getEnv("DB_PASSWORD", ""); pw != "" {
		userInfo += ":" + pw
	}
	pgxDSN := fmt.Sprintf(
		"postgres://%s@%s:%s/%s?sslmode=%s",
		userInfo,
		getEnv("DB_HOST", "localhost"),
		getEnv("DB_PORT", "5432"),
		getEnv("DB_NAME", "whatify"),
		getEnv("DB_SSLMODE", "disable"),
	)
	_ = dsn

	c, err := sqlstore.New(context.Background(), "pgx", pgxDSN, waLog.Noop)
	if err != nil {
		return fmt.Errorf("whatsmeow store: %w", err)
	}
	Container = c
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
