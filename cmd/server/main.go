package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/admin"
	"whatify/backend/internal/analytics"
	"whatify/backend/internal/auth"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/campaigns"
	"whatify/backend/internal/contacts"
	"whatify/backend/internal/developer"
	"whatify/backend/internal/flows"
	"whatify/backend/internal/funnels"
	"whatify/backend/internal/inbox"
	"whatify/backend/internal/products"
	quickreplies "whatify/backend/internal/quick_replies"
	"whatify/backend/internal/settings"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/internal/tags"
	"whatify/backend/internal/ai"
	"whatify/backend/internal/middleware"
	"whatify/backend/pkg/database"
	"whatify/backend/pkg/jwtutil"
	"whatify/backend/pkg/whatsapp"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// Try loading .env from several candidate paths so the server starts correctly
	// regardless of which directory it is launched from.
	envLoaded := false
	for _, path := range []string{".env", "backend/.env", "../../.env", "../../../.env"} {
		if err := godotenv.Load(path); err == nil {
			slog.Info("loaded env file", "path", path)
			envLoaded = true
			break
		}
	}
	if !envLoaded {
		slog.Info("no .env file found, using environment variables")
	}

	// Production mode: suppress debug route logs, enable optimisations.
	if os.Getenv("GIN_MODE") == "" {
		os.Setenv("GIN_MODE", "release")
	}
	gin.SetMode(os.Getenv("GIN_MODE"))

	// Validate required environment variables before starting.
	validateConfig()

	// Refuse to start with an insecure JWT secret
	jwtutil.ValidateSecret()

	database.Connect()

	if err := whatsapp.InitStore(); err != nil {
		slog.Error("whatsapp store init failed", "error", err)
		os.Exit(1)
	}

	// Wire incoming WhatsApp messages and history sync to the inbox handler
	session.Mgr.SetMessageHandler(inbox.HandleIncoming)
	session.Mgr.SetHistorySyncHandler(inbox.HandleHistorySync)
	inbox.FetchAvatar = session.Mgr.GetAvatarURL
	inbox.ResolveContactLID = session.Mgr.ResolveContactLID
	inbox.FetchContactInfo = session.Mgr.GetContactInfo

	// Wire funnel auto-advance into inbox
	inbox.FunnelReplyHandler = funnels.HandleInboundReply

	// Wire flow automation into inbox
	inbox.FlowHandler = flows.HandleIncoming

	// Backfill session_phone on existing conversations (one-time migration, safe to repeat)
	inbox.MigrateSessionPhone()

	// Wire session disconnect into admin package
	admin.DisconnectSession = session.Mgr.Disconnect

	// Auto-create super admin from env if not exists
	if saEmail := os.Getenv("SUPER_ADMIN_EMAIL"); saEmail != "" {
		if saPass := os.Getenv("SUPER_ADMIN_PASSWORD"); saPass != "" {
			admin.EnsureSuperAdmin(saEmail, saPass)
		}
	}

	// ── Background workers with context for graceful shutdown ──────────────
	ctx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()

	// Reconnect sessions that were connected before last restart
	go session.Mgr.ReconnectAll()

	// Resume campaigns interrupted by server restart
	go campaigns.ResumeInterrupted()

	// Scheduled campaigns ticker (every 60s)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("PANIC in scheduled campaigns ticker", "panic", r)
			}
		}()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				campaigns.LaunchScheduled()
			}
		}
	}()

	// Daily message count reset — fires at the next midnight UTC then every 24h
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("PANIC in daily count reset", "panic", r)
			}
		}()
		now := time.Now().UTC()
		nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(nextMidnight)):
		}
		billing.ResetDailyCounts()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				billing.ResetDailyCounts()
			}
		}
	}()

	// FlowRun retention — delete runs older than 90 days, runs every 24h
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cutoff := time.Now().AddDate(0, 0, -90)
				if res := database.DB.Where("executed_at < ?", cutoff).Delete(&models.FlowRun{}); res.Error == nil && res.RowsAffected > 0 {
					slog.Info("flows: cleaned up old runs", "deleted", res.RowsAffected)
				}
			}
		}
	}()

	r := gin.New()
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		Output:    os.Stdout,
		Formatter: ginLogFormatter,
	}))
	r.Use(gin.Recovery())

	// CORS — restrict to configured origins in production.
	// Set ALLOWED_ORIGINS=https://app.yourdomain.com in .env (comma-separated).
	// Falls back to wildcard only when ALLOWED_ORIGINS is not set (local dev).
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
	r.Use(func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if allowedOrigins == "" {
			if os.Getenv("GIN_MODE") == "release" {
				slog.Warn("ALLOWED_ORIGINS not set in production mode — denying all cross-origin requests")
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "ALLOWED_ORIGINS must be set in production"})
				return
			}
			c.Header("Access-Control-Allow-Origin", "*")
		} else if origin != "" && originAllowed(origin, allowedOrigins) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	r.GET("/health", func(c *gin.Context) {
		sqlDB, err := database.DB.DB()
		if err != nil || sqlDB.Ping() != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": "database unreachable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Prometheus metrics endpoint
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Global rate limit: 300 requests/minute per IP across all API endpoints.
	// Auth endpoints have their own tighter limit (10/min) applied on top.
	globalRL := middleware.RateLimit(300, time.Minute)

	v1 := r.Group("/api/v1", globalRL)
	auth.RegisterRoutes(v1)
	admin.RegisterRoutes(v1)
	session.RegisterRoutes(v1)
	inbox.RegisterRoutes(v1)
	contacts.RegisterRoutes(v1)
	tags.RegisterRoutes(v1)
	analytics.RegisterRoutes(v1)
	campaigns.RegisterRoutes(v1)
	funnels.RegisterRoutes(v1)
	flows.RegisterRoutes(v1)
	activity.RegisterRoutes(v1)
	products.RegisterRoutes(v1)
	quickreplies.RegisterRoutes(v1)
	settings.RegisterRoutes(v1)
	ai.RegisterRoutes(v1)
	developer.RegisterRoutes(v1)
	billing.RegisterRoutes(v1)
	billing.SetupPayPalPlans()
	billing.SeedBuiltinPlans()
	billing.FixTrialDates()
	funnels.StartTimeoutWorker(ctx)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in background so we can listen for shutdown signals.
	go func() {
		slog.Info("server starting", "port", port, "mode", gin.Mode())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Block until SIGINT or SIGTERM received.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("shutdown signal received — draining in-flight requests (30s max)")

	// Cancel all background workers first
	cancelWorkers()

	// Drain HTTP connections
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	slog.Info("server exited cleanly")
}

// validateConfig ensures required environment variables are set before starting.
func validateConfig() {
	required := []string{"DB_HOST", "DB_USER", "DB_PASSWORD", "DB_NAME", "JWT_SECRET"}
	for _, k := range required {
		if os.Getenv(k) == "" {
			slog.Error("required environment variable not set", "var", k)
			os.Exit(1)
		}
	}
	if os.Getenv("GIN_MODE") == "release" {
		prodRequired := []string{"ALLOWED_ORIGINS", "SUPER_ADMIN_PASSWORD"}
		for _, k := range prodRequired {
			if os.Getenv(k) == "" {
				slog.Error("production environment variable not set", "var", k)
				os.Exit(1)
			}
		}
	}
}

// originAllowed checks whether origin is in the comma-separated allowedOrigins list.
func originAllowed(origin, allowedOrigins string) bool {
	for _, allowed := range strings.Split(allowedOrigins, ",") {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	return false
}

// ginLogFormatter provides structured JSON-like log output for gin.
func ginLogFormatter(params gin.LogFormatterParams) string {
	slog.Info("http request",
		"status", params.StatusCode,
		"method", params.Method,
		"path", params.Path,
		"latency_ms", params.Latency.Milliseconds(),
		"client_ip", params.ClientIP,
		"error", params.ErrorMessage,
	)
	return ""
}
