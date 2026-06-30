package flows

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func decodeMedia(b64 string) ([]byte, error) {
	s := b64
	if idx := strings.Index(s, ","); idx != -1 {
		s = s[idx+1:]
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil {
		return nil, err
	}
	const maxBytes = 16 * 1024 * 1024
	if len(data) > maxBytes {
		return nil, fmt.Errorf("media too large (max 16 MB)")
	}
	return data, nil
}

func listFlows(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var flows []models.Flow
	database.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&flows)
	c.JSON(http.StatusOK, flows)
}

func getFlow(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	var flow models.Flow
	if err := database.DB.Where("id = ? AND tenant_id = ?", c.Param("id"), tenantID).First(&flow).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		return
	}
	c.JSON(http.StatusOK, flow)
}

type FlowInput struct {
	Name             string `json:"name" binding:"required"`
	Trigger          string `json:"trigger" binding:"required"`
	Keyword          string `json:"keyword"`
	KeywordMatchType string `json:"keyword_match_type"` // contains | exact | starts_with
	CooldownSeconds  int    `json:"cooldown_seconds"`
	SessionPhone     string `json:"session_phone"`
	Nodes            string `json:"nodes"`
	MediaBase64      string `json:"media_base64"`
	MediaMime        string `json:"media_mime"`
	MediaName        string `json:"media_name"`
	IsActive         *bool  `json:"is_active"`
}

func createFlow(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	var input FlowInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if creating this flow as active would exceed the plan limit.
	isActive := input.IsActive == nil || *input.IsActive
	if isActive {
		if err := billing.CheckFlowLimit(tenantID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}

	nodes := input.Nodes
	if nodes == "" {
		nodes = "[]"
	}
	active := true
	if input.IsActive != nil {
		active = *input.IsActive
	}

	kmt := input.KeywordMatchType
	if kmt == "" {
		kmt = "contains"
	}

	flow := models.Flow{
		TenantID:         tenantID,
		Name:             input.Name,
		Trigger:          models.FlowTrigger(input.Trigger),
		Keyword:          input.Keyword,
		KeywordMatchType: kmt,
		CooldownSeconds:  input.CooldownSeconds,
		SessionPhone:     input.SessionPhone,
		Nodes:            nodes,
		MediaMime:        input.MediaMime,
		MediaName:        input.MediaName,
		IsActive:         active,
	}
	if input.MediaBase64 != "" {
		data, err := decodeMedia(input.MediaBase64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid media_base64"})
			return
		}
		flow.MediaPayload = data
	}
	if err := database.DB.Create(&flow).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create flow"})
		return
	}
	activity.Log(tenantID, &userID, "flow.created", "flow", flow.ID.String(), map[string]string{
		"name":    flow.Name,
		"trigger": string(flow.Trigger),
	})
	c.JSON(http.StatusCreated, flow)
}

func updateFlow(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	var flow models.Flow
	if err := database.DB.Where("id = ? AND tenant_id = ?", c.Param("id"), tenantID).First(&flow).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		return
	}

	var input FlowInput
	c.ShouldBindJSON(&input)

	if input.Name != "" {
		flow.Name = input.Name
	}
	if input.Trigger != "" {
		flow.Trigger = models.FlowTrigger(input.Trigger)
	}
	flow.Keyword = input.Keyword
	if input.KeywordMatchType != "" {
		flow.KeywordMatchType = input.KeywordMatchType
	}
	flow.CooldownSeconds = input.CooldownSeconds
	flow.SessionPhone = input.SessionPhone
	if input.Nodes != "" {
		flow.Nodes = input.Nodes
	}
	// Media: empty string = clear, omitted/unchanged = keep existing
	if input.MediaBase64 == "" && input.MediaMime == "" {
		// keep existing media untouched
	} else if input.MediaBase64 == "" {
		// explicit clear
		flow.MediaPayload = nil
		flow.MediaMime = ""
		flow.MediaName = ""
	} else {
		data, err := decodeMedia(input.MediaBase64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid media_base64"})
			return
		}
		flow.MediaPayload = data
		flow.MediaMime = input.MediaMime
		flow.MediaName = input.MediaName
	}
	if input.IsActive != nil {
		flow.IsActive = *input.IsActive
	}
	database.DB.Save(&flow)
	activity.Log(tenantID, &userID, "flow.updated", "flow", flow.ID.String(), map[string]string{
		"name":    flow.Name,
		"trigger": string(flow.Trigger),
	})
	c.JSON(http.StatusOK, flow)
}

func deleteFlow(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	var flow models.Flow
	if err := database.DB.Where("id = ? AND tenant_id = ?", c.Param("id"), tenantID).First(&flow).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		return
	}
	database.DB.Delete(&flow)
	activity.Log(tenantID, &userID, "flow.deleted", "flow", flow.ID.String(), map[string]string{
		"name": flow.Name,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}

func listFlowRuns(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	flowID := c.Param("id")

	var flow models.Flow
	if err := database.DB.Where("id = ? AND tenant_id = ?", flowID, tenantID).First(&flow).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit > 100 {
		limit = 100
	}
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit

	var runs []models.FlowRun
	var total int64
	database.DB.Model(&models.FlowRun{}).Where("flow_id = ?", flowID).Count(&total)
	database.DB.Where("flow_id = ?", flowID).
		Order("executed_at DESC").
		Limit(limit).Offset(offset).
		Find(&runs)

	// Enrich with contact info
	for i := range runs {
		if runs[i].ContactID != nil {
			var contact models.Contact
			if database.DB.Select("name, push_name, phone_number").
				First(&contact, runs[i].ContactID).Error == nil {
				name := contact.Name
				if name == "" {
					name = contact.PushName
				}
				runs[i].ContactName = name
				runs[i].ContactPhone = contact.PhoneNumber
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"runs":  runs,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

func toggleFlow(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	var flow models.Flow
	if err := database.DB.Where("id = ? AND tenant_id = ?", c.Param("id"), tenantID).First(&flow).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		return
	}
	newState := !flow.IsActive
	database.DB.Model(&flow).Update("is_active", newState)
	action := "flow.enabled"
	if !newState {
		action = "flow.disabled"
	}
	activity.Log(tenantID, &userID, action, "flow", flow.ID.String(), map[string]string{
		"name": flow.Name,
	})
	c.JSON(http.StatusOK, gin.H{"is_active": newState})
}
