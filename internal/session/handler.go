package session

import (
	"fmt"
	"io"
	"net/http"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RegisterRoutes(r *gin.RouterGroup) {
	s := r.Group("/sessions", middleware.Auth())
	{
		// Read access is shared with agents (used by the inbox session switcher).
		s.GET("", handleList)

		// Connecting/disconnecting WhatsApp numbers is admin-only.
		admin := s.Group("", middleware.RequireAdmin())
		admin.POST("", handleCreate)
		admin.PUT("/:id", handleUpdate)
		admin.DELETE("/:id", handleDelete)
		admin.GET("/:id/qr", handleQR)
		admin.POST("/:id/reconnect", handleReconnect)
	}
}

func handleList(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	sessions, err := list(tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sessions)
}

func handleCreate(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)

	if err := billing.CheckSessionLimit(tenantID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s, err := create(tenantID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	activity.Log(tenantID, &userID, "session.created", "session", s.ID.String(), nil)

	// start whatsmeow connection in background
	go func() {
		_ = Mgr.Connect(s.ID)
	}()

	c.JSON(http.StatusCreated, toDTO(*s))
}

func handleUpdate(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var req struct {
		ProxyURL *string `json:"proxy_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sess, err := updateSession(tenantID, id, req.ProxyURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toDTO(*sess))
}

func handleDelete(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	id := c.Param("id")

	// fetch phone before delete for metadata
	var sess models.WhatsAppSession
	database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&sess)

	if err := deleteSession(tenantID, id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	activity.Log(tenantID, &userID, "session.deleted", "session", id, map[string]string{
		"phone": sess.Phone,
	})
	c.JSON(http.StatusOK, gin.H{"message": "session deleted"})
}

// handleReconnect tries to reconnect a DISCONNECTED session that still has a stored device.
// Returns 400 if the session has no phone (never paired) — caller should use QR flow instead.
func handleReconnect(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	sessionID, err := uuid.Parse(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session id"})
		return
	}

	var s models.WhatsAppSession
	if err := database.DB.Where("id = ? AND tenant_id = ?", sessionID, tenantID).First(&s).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if s.Phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no stored device — use QR scan instead"})
		return
	}

	go Mgr.Reconnect(s.ID, s.Phone)
	c.JSON(http.StatusOK, gin.H{"message": "reconnecting"})
}

// handleQR streams QR code updates via SSE.
func handleQR(c *gin.Context) {
	id := c.Param("id")

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	ch := Mgr.SubscribeQR(id)
	defer Mgr.UnsubscribeQR(id, ch)

	clientGone := c.Request.Context().Done()

	c.Stream(func(w io.Writer) bool {
		select {
		case <-clientGone:
			return false
		case update, ok := <-ch:
			if !ok {
				return false
			}
			switch update.Event {
			case "code":
				fmt.Fprintf(w, "event: qr\ndata: %s\n\n", update.Code)
			case "success":
				fmt.Fprintf(w, "event: success\ndata: connected\n\n")
				return false
			case "duplicate":
				fmt.Fprintf(w, "event: duplicate\ndata: duplicate\n\n")
				return false
			case "timeout":
				fmt.Fprintf(w, "event: timeout\ndata: timeout\n\n")
				return false
			case "error":
				fmt.Fprintf(w, "event: error\ndata: error\n\n")
				return false
			}
			return true
		}
	})
}
