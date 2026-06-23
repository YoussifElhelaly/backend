package tags

import (
	"net/http"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type TagInput struct {
	Name  string `json:"name" binding:"required"`
	Color string `json:"color"`
}

func listTags(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var tags []models.Tag
	if err := database.DB.Where("tenant_id = ?", tenantID).Find(&tags).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch tags"})
		return
	}
	c.JSON(http.StatusOK, tags)
}

func createTag(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var input TagInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	color := input.Color
	if color == "" {
		color = "#3B82F6"
	}

	tag := models.Tag{
		TenantID: tenantID,
		Name:     input.Name,
		Color:    color,
	}

	if err := database.DB.Create(&tag).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create tag"})
		return
	}
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	activity.Log(tenantID, &userID, "tag.created", "tag", tag.ID.String(), map[string]string{
		"name": tag.Name,
	})
	c.JSON(http.StatusCreated, tag)
}

func updateTag(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	tagID := c.Param("id")

	var input TagInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var tag models.Tag
	if err := database.DB.Where("id = ? AND tenant_id = ?", tagID, tenantID).First(&tag).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Tag not found"})
		return
	}

	tag.Name = input.Name
	if input.Color != "" {
		tag.Color = input.Color
	}

	database.DB.Save(&tag)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	activity.Log(tenantID, &userID, "tag.updated", "tag", tag.ID.String(), map[string]string{
		"name": tag.Name,
	})
	c.JSON(http.StatusOK, tag)
}

func deleteTag(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	tagID := c.Param("id")

	// Since we have a many-to-many relationship, GORM's delete won't automatically clean the join table unless we clear associations.
	// We'll manually delete associations or just let cascade constraints (if any) handle it.
	// To be safe, we can clear associations first.
	var tag models.Tag
	if err := database.DB.Where("id = ? AND tenant_id = ?", tagID, tenantID).First(&tag).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Tag not found"})
		return
	}

	database.DB.Model(&tag).Association("Contacts").Clear()
	database.DB.Delete(&tag)

	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	activity.Log(tenantID, &userID, "tag.deleted", "tag", tag.ID.String(), map[string]string{
		"name": tag.Name,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Tag deleted"})
}
