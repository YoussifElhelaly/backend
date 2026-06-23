package activity

import (
	"net/http"
	"strconv"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ActivityResponse struct {
	models.ActivityLog
	UserName string `json:"user_name"`
}

func listActivity(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 30
	}
	offset := (page - 1) * limit

	action := c.Query("action")
	entityType := c.Query("entity_type")

	var total int64
	q := database.DB.Model(&models.ActivityLog{}).Where("tenant_id = ?", tenantID)
	if action != "" {
		q = q.Where("action = ?", action)
	}
	if entityType != "" {
		q = q.Where("entity_type = ?", entityType)
	}
	q.Count(&total)

	var logs []models.ActivityLog
	q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&logs)

	// Enrich with user names
	userCache := map[uuid.UUID]string{}
	results := make([]ActivityResponse, 0, len(logs))
	for _, l := range logs {
		resp := ActivityResponse{ActivityLog: l}
		if l.UserID != nil {
			if name, ok := userCache[*l.UserID]; ok {
				resp.UserName = name
			} else {
				var u models.User
				if database.DB.Select("name").First(&u, "id = ?", *l.UserID).Error == nil {
					userCache[*l.UserID] = u.Name
					resp.UserName = u.Name
				}
			}
		}
		results = append(results, resp)
	}

	c.JSON(http.StatusOK, gin.H{
		"items": results,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}
