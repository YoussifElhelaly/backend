package quick_replies

import (
	"net/http"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type QuickReplyInput struct {
	Title   string `json:"title" binding:"required"`
	Content string `json:"content" binding:"required"`
}

func list(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	q := c.Query("q")

	query := database.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC")
	if q != "" {
		query = query.Where("title ILIKE ? OR content ILIKE ?", "%"+q+"%", "%"+q+"%")
	}

	var replies []models.QuickReply
	if err := query.Find(&replies).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch quick replies"})
		return
	}
	c.JSON(http.StatusOK, replies)
}

func create(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var input QuickReplyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := billing.CheckQuickReplyLimit(tenantID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	reply := models.QuickReply{
		TenantID: tenantID,
		Title:    input.Title,
		Content:  input.Content,
	}

	if err := database.DB.Create(&reply).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create quick reply"})
		return
	}
	c.JSON(http.StatusCreated, reply)
}

func update(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	replyID := c.Param("id")

	var reply models.QuickReply
	if err := database.DB.Where("id = ? AND tenant_id = ?", replyID, tenantID).First(&reply).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Quick reply not found"})
		return
	}

	var input QuickReplyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	reply.Title = input.Title
	reply.Content = input.Content

	database.DB.Save(&reply)
	c.JSON(http.StatusOK, reply)
}

func remove(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	replyID := c.Param("id")

	var reply models.QuickReply
	if err := database.DB.Where("id = ? AND tenant_id = ?", replyID, tenantID).First(&reply).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Quick reply not found"})
		return
	}

	database.DB.Delete(&reply)
	c.JSON(http.StatusOK, gin.H{"message": "Quick reply deleted"})
}
