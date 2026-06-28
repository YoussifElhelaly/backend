package whatsapp

import (
	"context"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

var Container *sqlstore.Container

// configureFullHistorySync makes WhatsApp push the FULL chat history on pairing
// (like WhatsApp Web's "syncing older chats"), instead of just the recent window.
// These DeviceProps are read at QR-pairing time, so this only affects NEW pairings —
// existing paired devices won't re-sync. WhatsApp caps full sync at ~1 year regardless.
func configureFullHistorySync() {
	store.DeviceProps.RequireFullSync = proto.Bool(true)
	if store.DeviceProps.HistorySyncConfig == nil {
		return
	}
	hs := store.DeviceProps.HistorySyncConfig
	hs.FullSyncDaysLimit = proto.Uint32(3650)    // ask for as far back as WhatsApp allows
	hs.FullSyncSizeMbLimit = proto.Uint32(102400) // don't cap by size
	hs.StorageQuotaMb = proto.Uint32(102400)
}

func InitStore() error {
	configureFullHistorySync()

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

	// whatsmeow sqlstore expects a URL-style DSN for pgx.
	// Include password in userinfo if set (percent-encode not needed for typical passwords).
	userInfo := getEnv("DB_USER", "whatify")
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
