package main

import (
	"context"
	"log"
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
	"whatify/backend/internal/flows"
	"whatify/backend/internal/funnels"
	"whatify/backend/internal/inbox"
	"whatify/backend/internal/products"
	quickreplies "whatify/backend/internal/quick_replies"
	"whatify/backend/internal/settings"
	"whatify/backend/internal/session"
	"whatify/backend/internal/tags"
	"whatify/backend/internal/middleware"
	"whatify/backend/pkg/database"
	"whatify/backend/pkg/jwtutil"
	"whatify/backend/pkg/whatsapp"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, using environment variables")
	}

	// Production mode: suppress debug route logs, enable optimisations.
	if os.Getenv("GIN_MODE") == "" {
		os.Setenv("GIN_MODE", "release")
	}
	gin.SetMode(os.Getenv("GIN_MODE"))

	// Refuse to start with an insecure JWT secret
	jwtutil.ValidateSecret()

	database.Connect()

	if err := whatsapp.InitStore(); err != nil {
		log.Fatalf("whatsapp store: %v", err)
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

	// Auto-create super admin from env if not exists
	if saEmail := os.Getenv("SUPER_ADMIN_EMAIL"); saEmail != "" {
		if saPass := os.Getenv("SUPER_ADMIN_PASSWORD"); saPass != "" {
			admin.EnsureSuperAdmin(saEmail, saPass)
		}
	}

	// Reconnect sessions that were connected before last restart
	go session.Mgr.ReconnectAll()

	// Resume campaigns interrupted by server restart
	go campaigns.ResumeInterrupted()

	// Scheduled campaigns ticker (every 60s)
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		for range ticker.C {
			campaigns.LaunchScheduled()
		}
	}()

	// Daily message count reset — fires at the next midnight UTC then every 24h
	go func() {
		now := time.Now().UTC()
		nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		time.Sleep(time.Until(nextMidnight))
		billing.ResetDailyCounts()
		ticker := time.NewTicker(24 * time.Hour)
		for range ticker.C {
			billing.ResetDailyCounts()
		}
	}()

	r := gin.Default()

	// CORS — restrict to configured origins in production.
	// Set ALLOWED_ORIGINS=https://app.yourdomain.com in .env (comma-separated).
	// Falls back to wildcard only when ALLOWED_ORIGINS is not set (local dev).
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
	r.Use(func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if allowedOrigins == "" {
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
	billing.RegisterRoutes(v1)
	billing.SetupPayPalPlans()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	// Start server in background so we can listen for shutdown signals.
	go func() {
		log.Printf("server starting on :%s (mode=%s)", port, gin.Mode())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	// Block until SIGINT or SIGTERM received.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutdown signal received — draining in-flight requests (30s max)…")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	log.Println("server exited cleanly")
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
