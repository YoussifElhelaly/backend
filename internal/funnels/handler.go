package funnels

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"time"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── List ─────────────────────────────────────────────────────────────────────

func listFunnels(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)

	var funnels []models.Funnel
	database.DB.Preload("Steps").Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&funnels)

	for i := range funnels {
		var count int64
		database.DB.Model(&models.FunnelContact{}).
			Where("funnel_id = ? AND status = 'ACTIVE'", funnels[i].ID).Count(&count)
		funnels[i].ContactCount = int(count)
	}
	c.JSON(http.StatusOK, funnels)
}

// ── Get ──────────────────────────────────────────────────────────────────────

func getFunnel(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var funnel models.Funnel
	if err := database.DB.Preload("Steps").Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}
	c.JSON(http.StatusOK, funnel)
}

// ── Create ───────────────────────────────────────────────────────────────────

type StepInput struct {
	Name        string   `json:"name" binding:"required"`
	Type        string   `json:"type" binding:"required"`
	Message     string   `json:"message"`
	Variants    []string `json:"variants"` // AI-approved message variants
	MediaBase64 string   `json:"media_base64"` // optional image (raw base64 or data URL)
	MediaMime   string   `json:"media_mime"`
	MediaName   string   `json:"media_name"`
}

type CreateFunnelInput struct {
	Name             string      `json:"name" binding:"required"`
	SessionPhone     string      `json:"session_phone" binding:"required"`
	Description      string      `json:"description"`
	ReplyWindowHours int         `json:"reply_window_hours"`
	TimeoutAction    string      `json:"timeout_action"`
	FollowUpMessage  string      `json:"follow_up_message"`
	Steps            []StepInput `json:"steps" binding:"required,min=1"`
}

func createFunnel(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)

	var input CreateFunnelInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	rwh := input.ReplyWindowHours
	if rwh <= 0 {
		rwh = 48
	}

	funnel := models.Funnel{
		TenantID:         tenantID,
		SessionPhone:     input.SessionPhone,
		Name:             input.Name,
		Description:      input.Description,
		Status:           models.FunnelStatusDraft,
		ReplyWindowHours: rwh,
		TimeoutAction:    models.FunnelTimeoutAction(input.TimeoutAction),
		FollowUpMessage:  input.FollowUpMessage,
	}
	if err := database.DB.Create(&funnel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create funnel"})
		return
	}

	for i, s := range input.Steps {
		stepType := models.FunnelStepType(s.Type)
		media, mime, err := decodeMedia(s.MediaBase64, s.MediaMime)
		if err != nil {
			database.DB.Delete(&funnel)
			c.JSON(http.StatusBadRequest, gin.H{"error": "step " + s.Name + ": " + err.Error()})
			return
		}
		variantsJSON := "[]"
		if len(s.Variants) > 0 {
			if b, err := json.Marshal(s.Variants); err == nil {
				variantsJSON = string(b)
			}
		}
		step := models.FunnelStep{
			FunnelID:     funnel.ID,
			Order:        i + 1,
			Name:         s.Name,
			Type:         stepType,
			Message:      s.Message,
			Variants:     variantsJSON,
			MediaPayload: media,
			MediaMime:    mime,
			MediaName:    s.MediaName,
		}
		if err := database.DB.Create(&step).Error; err != nil {
			slog.Error("funnels: failed to create step", "step_name", s.Name, "funnel_id", funnel.ID, "error", err)
			database.DB.Delete(&funnel)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create step: " + s.Name})
			return
		}
		funnel.Steps = append(funnel.Steps, step)
	}

	activity.Log(tenantID, &userID, "funnel.created", "funnel", funnel.ID.String(), map[string]string{
		"name": funnel.Name,
	})
	c.JSON(http.StatusCreated, funnel)
}

// ── Update ───────────────────────────────────────────────────────────────────

func updateFunnel(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}

	var input struct {
		Name             *string `json:"name"`
		Description      *string `json:"description"`
		ReplyWindowHours *int    `json:"reply_window_hours"`
		TimeoutAction    *string `json:"timeout_action"`
		FollowUpMessage  *string `json:"follow_up_message"`
	}
	c.ShouldBindJSON(&input)
	if input.Name != nil {
		funnel.Name = *input.Name
	}
	if input.Description != nil {
		funnel.Description = *input.Description
	}
	if input.ReplyWindowHours != nil && *input.ReplyWindowHours > 0 {
		funnel.ReplyWindowHours = *input.ReplyWindowHours
	}
	if input.TimeoutAction != nil {
		funnel.TimeoutAction = models.FunnelTimeoutAction(*input.TimeoutAction)
	}
	if input.FollowUpMessage != nil {
		funnel.FollowUpMessage = *input.FollowUpMessage
	}
	database.DB.Save(&funnel)
	c.JSON(http.StatusOK, funnel)
}

// ── Delete ───────────────────────────────────────────────────────────────────

func deleteFunnel(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}

	database.DB.Where("funnel_id = ?", funnel.ID).Delete(&models.FunnelContactHistory{})
	database.DB.Where("funnel_id = ?", funnel.ID).Delete(&models.FunnelContact{})
	database.DB.Where("funnel_id = ?", funnel.ID).Delete(&models.FunnelStep{})
	database.DB.Delete(&funnel)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	activity.Log(tenantID, &userID, "funnel.deleted", "funnel", funnel.ID.String(), map[string]string{
		"name": funnel.Name,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Deleted"})
}

// ── Activate / Pause ─────────────────────────────────────────────────────────

func activateFunnel(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	id := c.Param("id")
	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}
	database.DB.Model(&funnel).Update("status", models.FunnelStatusActive)
	activity.Log(tenantID, &userID, "funnel.activated", "funnel", funnel.ID.String(), map[string]string{"name": funnel.Name})
	c.JSON(http.StatusOK, gin.H{"status": "ACTIVE"})
}

func pauseFunnel(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	id := c.Param("id")
	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}
	database.DB.Model(&funnel).Update("status", models.FunnelStatusPaused)
	activity.Log(tenantID, &userID, "funnel.paused", "funnel", funnel.ID.String(), map[string]string{"name": funnel.Name})
	c.JSON(http.StatusOK, gin.H{"status": "PAUSED"})
}

// ── Add Step ─────────────────────────────────────────────────────────────────

func addStep(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}

	var input StepInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var maxOrder int
	database.DB.Model(&models.FunnelStep{}).Where("funnel_id = ?", funnel.ID).
		Select("COALESCE(MAX(\"order\"), 0)").Scan(&maxOrder)

	media, mime, err := decodeMedia(input.MediaBase64, input.MediaMime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "media: " + err.Error()})
		return
	}
	variantsJSON := "[]"
	if len(input.Variants) > 0 {
		if b, _ := json.Marshal(input.Variants); b != nil {
			variantsJSON = string(b)
		}
	}
	step := models.FunnelStep{
		FunnelID:     funnel.ID,
		Order:        maxOrder + 1,
		Name:         input.Name,
		Type:         models.FunnelStepType(input.Type),
		Message:      input.Message,
		Variants:     variantsJSON,
		MediaPayload: media,
		MediaMime:    mime,
		MediaName:    input.MediaName,
	}
	database.DB.Create(&step)
	c.JSON(http.StatusCreated, step)
}

// ── Update Step ──────────────────────────────────────────────────────────────

func updateStep(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")
	stepID := c.Param("step_id")

	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}

	var step models.FunnelStep
	if err := database.DB.Where("id = ? AND funnel_id = ?", stepID, funnel.ID).First(&step).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Step not found"})
		return
	}

	var input struct {
		Name        string   `json:"name"`
		Type        string   `json:"type"`
		Message     string   `json:"message"`
		Variants    []string `json:"variants"`
		MediaBase64 string   `json:"media_base64"`
		MediaMime   string   `json:"media_mime"`
		MediaName   string   `json:"media_name"`
	}
	c.ShouldBindJSON(&input)

	if input.Name != "" {
		step.Name = input.Name
	}
	if input.Type != "" {
		step.Type = models.FunnelStepType(input.Type)
	}
	if input.Message != "" {
		step.Message = input.Message
	}
	if input.Variants != nil {
		if b, _ := json.Marshal(input.Variants); b != nil {
			step.Variants = string(b)
		}
	}
	if input.MediaBase64 != "" {
		media, mime, err := decodeMedia(input.MediaBase64, input.MediaMime)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		step.MediaPayload = media
		step.MediaMime = mime
		if input.MediaName != "" {
			step.MediaName = input.MediaName
		}
	}
	database.DB.Save(&step)
	c.JSON(http.StatusOK, step)
}

// ── Delete Step ──────────────────────────────────────────────────────────────

func deleteStep(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")
	stepID := c.Param("step_id")

	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}

	var step models.FunnelStep
	if err := database.DB.Where("id = ? AND funnel_id = ?", stepID, funnel.ID).First(&step).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Step not found"})
		return
	}

	// Don't allow deleting ENTRY or REPLY_TRIGGER steps (they're structural)
	if step.Type == models.FunnelStepEntry || step.Type == models.FunnelStepReplyTrigger {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot delete structural steps (Message Sent / Replied)"})
		return
	}

	// Check if any contacts are currently on this step
	var contactCount int64
	database.DB.Model(&models.FunnelContact{}).
		Where("funnel_id = ? AND current_step_id = ?", funnel.ID, stepID).Count(&contactCount)
	if contactCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Move contacts off this step before deleting it"})
		return
	}

	database.DB.Delete(&step)

	// Re-order remaining steps
	var remaining []models.FunnelStep
	database.DB.Where("funnel_id = ? AND \"order\" > ?", funnel.ID, step.Order).Order("\"order\" ASC").Find(&remaining)
	for i, s := range remaining {
		database.DB.Model(&s).Update("order", step.Order+i)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Step deleted"})
}

// ── Contact History ──────────────────────────────────────────────────────────

func getContactHistory(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")
	cid := c.Param("contact_id")

	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}

	var history []models.FunnelContactHistory
	database.DB.Preload("FromStep").Preload("ToStep").
		Where("funnel_id = ? AND contact_id = ?", funnel.ID, cid).
		Order("created_at ASC").Find(&history)

	c.JSON(http.StatusOK, history)
}

// ── Pipeline (Kanban) ─────────────────────────────────────────────────────────

type PipelineStep struct {
	models.FunnelStep
	Contacts []models.FunnelContact `json:"contacts"`
}

func getPipeline(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var funnel models.Funnel
	if err := database.DB.Preload("Steps").Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}

	sort.Slice(funnel.Steps, func(i, j int) bool {
		return funnel.Steps[i].Order < funnel.Steps[j].Order
	})

	// Stats — lightweight COUNT queries (no record loading)
	var total, active, converted, dropped int64
	database.DB.Model(&models.FunnelContact{}).Where("funnel_id = ?", funnel.ID).Count(&total)
	database.DB.Model(&models.FunnelContact{}).Where("funnel_id = ? AND status = 'ACTIVE'", funnel.ID).Count(&active)
	database.DB.Model(&models.FunnelContact{}).Where("funnel_id = ? AND status = 'CONVERTED'", funnel.ID).Count(&converted)
	database.DB.Model(&models.FunnelContact{}).Where("funnel_id = ? AND status = 'DROPPED'", funnel.ID).Count(&dropped)

	// Contacts — paginated to avoid OOM on large funnels
	contactLimit := 500
	if v := c.Query("contact_limit"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &contactLimit); err != nil || n != 1 || contactLimit < 1 {
			contactLimit = 500
		}
	}
	if contactLimit > 2000 {
		contactLimit = 2000
	}
	contactOffset := 0
	if v := c.Query("contact_offset"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &contactOffset); err != nil || n != 1 || contactOffset < 0 {
			contactOffset = 0
		}
	}

	var fcs []models.FunnelContact
	database.DB.Preload("Contact.Tags").Preload("CurrentStep").
		Where("funnel_id = ?", funnel.ID).
		Order("last_moved_at DESC").
		Offset(contactOffset).Limit(contactLimit + 1).
		Find(&fcs)

	hasMore := len(fcs) > contactLimit
	if hasMore {
		fcs = fcs[:contactLimit]
	}

	stepMap := map[uuid.UUID]*PipelineStep{}
	result := make([]PipelineStep, 0, len(funnel.Steps))
	for _, s := range funnel.Steps {
		ps := PipelineStep{FunnelStep: s, Contacts: []models.FunnelContact{}}
		result = append(result, ps)
		stepMap[s.ID] = &result[len(result)-1]
	}

	for _, fc := range fcs {
		if ps, ok := stepMap[fc.CurrentStepID]; ok {
			ps.Contacts = append(ps.Contacts, fc)
		}
	}

	convRate := 0.0
	if total > 0 {
		convRate = float64(converted) / float64(total) * 100
	}

	c.JSON(http.StatusOK, gin.H{
		"funnel":          funnel,
		"steps":           result,
		"total_contacts":  total,
		"active":          active,
		"converted":       converted,
		"dropped":         dropped,
		"conversion_rate": convRate,
		"has_more":        hasMore,
	})
}

// ── Launch ───────────────────────────────────────────────────────────────────

type LaunchInput struct {
	ContactIDs []uuid.UUID `json:"contact_ids"`
	TagIDs     []uuid.UUID `json:"tag_ids"`
}

func launchFunnel(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	id := c.Param("id")

	var funnel models.Funnel
	if err := database.DB.Preload("Steps").Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}

	// Find entry step
	var entryStep *models.FunnelStep
	for i, s := range funnel.Steps {
		if s.Type == models.FunnelStepEntry {
			entryStep = &funnel.Steps[i]
			break
		}
	}
	if entryStep == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No ENTRY step defined"})
		return
	}

	var input LaunchInput
	c.ShouldBindJSON(&input)

	// Collect contacts
	contactMap := map[uuid.UUID]bool{}
	for _, cid := range input.ContactIDs {
		contactMap[cid] = true
	}
	if len(input.TagIDs) > 0 {
		var tagContacts []models.Contact
		database.DB.
			Joins("JOIN contact_tags ON contacts.id = contact_tags.contact_id").
			Where("contact_tags.tag_id IN ? AND contacts.tenant_id = ?", input.TagIDs, tenantID).
			Find(&tagContacts)
		for _, ct := range tagContacts {
			contactMap[ct.ID] = true
		}
	}
	if len(contactMap) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No contacts selected"})
		return
	}

	// Validate contact count against daily message limit
	if err := billing.CheckCampaignLimitAtCreation(tenantID, funnel.SessionPhone, len(contactMap)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create campaign record for tracking
	now := time.Now()
	campaign := models.Campaign{
		TenantID:      tenantID,
		SessionPhone:  funnel.SessionPhone,
		Name:          "Funnel: " + funnel.Name,
		Message:       entryStep.Message,
		Status:        models.CampaignStatusDraft,
		FunnelID:      &funnel.ID,
		TotalContacts: len(contactMap),
	}
	database.DB.Create(&campaign)

	queued := 0
	skipped := 0

	// Fetch tenant delay settings before the loop (single query)
	var tenant models.Tenant
	database.DB.Select("campaign_delay_min, campaign_delay_max").Where("id = ?", tenantID).First(&tenant)
	dMin := tenant.CampaignDelayMin
	dMax := tenant.CampaignDelayMax
	if dMin < 1 {
		dMin = 3
	}
	if dMax <= dMin {
		dMax = dMin + 5
	}
	spread := dMax - dMin
	if spread == 0 {
		spread = 1
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("funnels: PANIC recovered in launch goroutine", "funnel_id", funnel.ID, "panic", r)
			}
		}()

		startedAt := time.Now()
		failedCount := 0

		for contactID := range contactMap {
			// Skip if already active in this funnel
			var existing models.FunnelContact
			if database.DB.Where("funnel_id = ? AND contact_id = ? AND status = 'ACTIVE'", funnel.ID, contactID).First(&existing).Error == nil {
				skipped++
				continue
			}

			// Skip if already active in ANY other funnel
			var inOtherFunnel models.FunnelContact
			if database.DB.Where("contact_id = ? AND status = 'ACTIVE' AND funnel_id != ?", contactID, funnel.ID).First(&inOtherFunnel).Error == nil {
				slog.Info("funnels: skipping contact — already active in another funnel", "contact_id", contactID, "funnel_id", inOtherFunnel.FunnelID)
				skipped++
				continue
			}

			var contact models.Contact
			if err := database.DB.First(&contact, "id = ?", contactID).Error; err != nil {
				skipped++
				continue
			}

			// Create FunnelContact
			fc := models.FunnelContact{
				FunnelID:      funnel.ID,
				ContactID:     contactID,
				CurrentStepID: entryStep.ID,
				Status:        models.FunnelContactActive,
				EnteredAt:     now,
				LastMovedAt:   now,
			}
			database.DB.Create(&fc)

			// History
			history := models.FunnelContactHistory{
				FunnelID:  funnel.ID,
				ContactID: contactID,
				ToStepID:  entryStep.ID,
				Trigger:   "MANUAL",
			}
			database.DB.Create(&history)

			// CampaignContact
			cc := models.CampaignContact{
				CampaignID: campaign.ID,
				ContactID:  contactID,
				Status:     models.CampaignContactPending,
			}
			database.DB.Create(&cc)

			queued++

			// Update total_contacts to reflect actual contacts entering (not pre-skip estimate)
			database.DB.Model(&campaign).Update("total_contacts", queued)

			// Send entry message if present
			if entryStep.Message != "" || len(entryStep.MediaPayload) > 0 {
				// Check daily limit before sending
				if limitErr := billing.CheckDailyMessageLimit(tenantID, funnel.SessionPhone); limitErr != nil {
					slog.Warn("funnels: daily limit reached, pausing", "funnel_id", funnel.ID, "error", limitErr)
					database.DB.Model(&campaign).Updates(map[string]interface{}{
						"status":   models.CampaignStatusPaused,
						"error_msg": limitErr.Error(),
					})
					return
				}

				err := sendStepViaSession(funnel.SessionPhone, tenantID, contact.PhoneNumber, *entryStep, contact)
				sentAt := time.Now()
				if err != nil {
					slog.Error("funnels: failed to send", "phone", contact.PhoneNumber, "error", err)
					failedCount++
					database.DB.Model(&cc).Updates(map[string]interface{}{
						"status":    models.CampaignContactFailed,
						"error_msg": err.Error(),
					})
					database.DB.Model(&campaign).Updates(map[string]interface{}{
						"failed_count": failedCount,
					})
				} else {
					billing.IncrementDailyCount(tenantID, funnel.SessionPhone)
					database.DB.Model(&cc).Updates(map[string]interface{}{
						"status":  models.CampaignContactSent,
						"sent_at": &sentAt,
					})
					database.DB.Model(&campaign).Updates(map[string]interface{}{
						"sent_count": campaign.SentCount + 1,
						"started_at": &startedAt,
					})
					campaign.SentCount++
				}
			}

			// Anti-spam jitter (uses tenant's configured delay range)
			jitter := time.Duration(dMin+rand.Intn(spread)) * time.Second
			time.Sleep(jitter)
		}

		completedAt := time.Now()
		database.DB.Model(&campaign).Updates(map[string]interface{}{
			"status":        models.CampaignStatusCompleted,
			"completed_at":  &completedAt,
			"total_contacts": queued, // Final accurate count after all skips
		})

		// Activate funnel if was DRAFT
		if funnel.Status == models.FunnelStatusDraft {
			database.DB.Model(&funnel).Update("status", models.FunnelStatusActive)
		}
	}()

	// Activate funnel immediately
	if funnel.Status == models.FunnelStatusDraft {
		database.DB.Model(&funnel).Update("status", models.FunnelStatusActive)
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "Funnel launched",
		"campaign_id":    campaign.ID,
		"total_selected": len(contactMap),
	})
}

// ── Move Contact ─────────────────────────────────────────────────────────────

func moveContact(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	id := c.Param("id")

	var input struct {
		ContactID uuid.UUID `json:"contact_id" binding:"required"`
		ToStepID  uuid.UUID `json:"to_step_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}

	var fc models.FunnelContact
	if err := database.DB.Where("funnel_id = ? AND contact_id = ?", funnel.ID, input.ContactID).First(&fc).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not in funnel"})
		return
	}

	// Verify target step belongs to this funnel
	var toStep models.FunnelStep
	if err := database.DB.Where("id = ? AND funnel_id = ?", input.ToStepID, funnel.ID).First(&toStep).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid target step"})
		return
	}

	fromStepID := fc.CurrentStepID
	now := time.Now()
	database.DB.Model(&fc).Updates(map[string]interface{}{
		"current_step_id": input.ToStepID,
		"last_moved_at":   now,
	})

	history := models.FunnelContactHistory{
		FunnelID:   funnel.ID,
		ContactID:  input.ContactID,
		FromStepID: &fromStepID,
		ToStepID:   input.ToStepID,
		Trigger:    "MANUAL",
		MovedBy:    &userID,
	}
	database.DB.Create(&history)

	// Send step message if present
	if toStep.Message != "" || len(toStep.MediaPayload) > 0 {
		// Check daily limit before sending
		if limitErr := billing.CheckDailyMessageLimit(tenantID, funnel.SessionPhone); limitErr != nil {
			slog.Warn("funnels: daily limit reached, skipping move send", "funnel_id", funnel.ID, "error", limitErr)
			c.JSON(http.StatusTooManyRequests, gin.H{"error": limitErr.Error()})
			return
		}
		var contact models.Contact
		if database.DB.First(&contact, "id = ?", input.ContactID).Error == nil {
			go func() {
				if err := sendStepViaSession(funnel.SessionPhone, tenantID, contact.PhoneNumber, toStep, contact); err == nil {
					billing.IncrementDailyCount(tenantID, funnel.SessionPhone)
				}
			}()
		}
	}

	var contact models.Contact
	database.DB.First(&contact, "id = ?", input.ContactID)
	activity.Log(tenantID, &userID, "funnel.contact_moved", "funnel", funnel.ID.String(), map[string]string{
		"funnel":    funnel.Name,
		"to_step":   toStep.Name,
		"contact":   contact.PhoneNumber,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Contact moved"})
}

// ── Set Contact Status ────────────────────────────────────────────────────────

func setContactStatus(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	id := c.Param("id")
	cid := c.Param("contact_id")

	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&funnel).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Funnel not found"})
		return
	}

	var input struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	status := models.FunnelContactStatus(input.Status)
	if status != models.FunnelContactActive && status != models.FunnelContactConverted && status != models.FunnelContactDropped {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status — must be ACTIVE, CONVERTED, or DROPPED"})
		return
	}

	database.DB.Model(&models.FunnelContact{}).
		Where("funnel_id = ? AND contact_id = ?", funnel.ID, cid).
		Update("status", status)

	action := "funnel.contact_" + strings.ToLower(input.Status)
	var contact models.Contact
	meta := map[string]string{"funnel": funnel.Name, "status": input.Status}
	if database.DB.First(&contact, "id = ?", cid).Error == nil {
		meta["contact"] = contact.PhoneNumber
	}
	activity.Log(tenantID, &userID, action, "funnel", funnel.ID.String(), meta)
	c.JSON(http.StatusOK, gin.H{"status": input.Status})
}

// ── Inbound Reply Handler ─────────────────────────────────────────────────────
// Called from inbox.service when an incoming message is received.

// HandleInboundReply is called when a contact sends a message. It checks whether
// the contact is active in a funnel and, if so, advances them to the next step.
// Returns true if the reply was consumed by a funnel (callers should skip flows).
func HandleInboundReply(contactID uuid.UUID, tenantID uuid.UUID) bool {
	slog.Debug("funnels: HandleInboundReply called", "contact_id", contactID, "tenant_id", tenantID)

	var fc models.FunnelContact
	if err := database.DB.
		Where("contact_id = ? AND status = 'ACTIVE'", contactID).
		First(&fc).Error; err != nil {
		// Log all funnel_contacts for this contact to debug
		var all []models.FunnelContact
		database.DB.Where("contact_id = ?", contactID).Find(&all)
		slog.Debug("funnels: no active funnel contact", "contact_id", contactID, "total_records", len(all))
		for _, r := range all {
			slog.Debug("funnels: funnel_contact record", "funnel_id", r.FunnelID, "step_id", r.CurrentStepID, "status", r.Status)
		}
		return false // contact not in any active funnel
	}
	slog.Debug("funnels: found active funnel contact",
		"fc_id", fc.ID, "funnel_id", fc.FunnelID, "current_step", fc.CurrentStepID,
		"status", fc.Status, "last_moved_at", fc.LastMovedAt)

	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", fc.FunnelID, tenantID).First(&funnel).Error; err != nil {
		slog.Debug("funnels: funnel not found or tenant mismatch", "funnel_id", fc.FunnelID, "error", err)
		return false
	}
	if funnel.Status != models.FunnelStatusActive {
		slog.Debug("funnels: funnel not ACTIVE", "funnel_id", funnel.ID, "status", funnel.Status)
		return false
	}

	// Check reply window — contact is still "in funnel" but window expired: return
	// true so flows don't fire either (contact is mid-funnel, just timed out).
	elapsed := time.Since(fc.LastMovedAt)
	windowDuration := time.Duration(funnel.ReplyWindowHours) * time.Hour
	slog.Debug("funnels: reply window check", "elapsed", elapsed, "window", windowDuration, "reply_window_hours", funnel.ReplyWindowHours)
	if elapsed > windowDuration {
		slog.Debug("funnels: reply window expired, skipping advance", "contact_id", contactID)
		return true
	}

	// Get current step
	var currentStep models.FunnelStep
	if err := database.DB.First(&currentStep, "id = ?", fc.CurrentStepID).Error; err != nil {
		slog.Debug("funnels: current step not found", "step_id", fc.CurrentStepID, "error", err)
		return true
	}
	slog.Debug("funnels: current step",
		"step_id", currentStep.ID, "name", currentStep.Name, "type", currentStep.Type, "order", currentStep.Order)

	// Get next step
	var nextStep models.FunnelStep
	if err := database.DB.Where("funnel_id = ? AND \"order\" = ?", funnel.ID, currentStep.Order+1).
		First(&nextStep).Error; err != nil {
		slog.Debug("funnels: no next step found", "funnel_id", funnel.ID, "order", currentStep.Order+1, "error", err)
		return true // no next step — funnel is done, still suppress flows
	}
	slog.Debug("funnels: next step",
		"step_id", nextStep.ID, "name", nextStep.Name, "type", nextStep.Type, "order", nextStep.Order)

	// Auto-advance when:
	// - next step is REPLY_TRIGGER (designed to receive replies, regardless of current step type)
	// - OR current step is ENTRY or REPLY_TRIGGER (they inherently wait for a reply)
	shouldAdvance := nextStep.Type == models.FunnelStepReplyTrigger ||
		currentStep.Type == models.FunnelStepEntry ||
		currentStep.Type == models.FunnelStepReplyTrigger
	if !shouldAdvance {
		slog.Debug("funnels: step not auto-advancable", "current_type", currentStep.Type, "next_type", nextStep.Type)
		return true // MANUAL → MANUAL: needs human intervention
	}

	fromStepID := fc.CurrentStepID
	now := time.Now()
	database.DB.Model(&fc).Updates(map[string]interface{}{
		"current_step_id": nextStep.ID,
		"last_moved_at":   now,
	})

	history := models.FunnelContactHistory{
		FunnelID:   funnel.ID,
		ContactID:  contactID,
		FromStepID: &fromStepID,
		ToStepID:   nextStep.ID,
		Trigger:    "AUTO_REPLY",
	}
	database.DB.Create(&history)

	// Send next step message if present
	if nextStep.Message != "" || len(nextStep.MediaPayload) > 0 {
		// Check daily limit before sending
		if limitErr := billing.CheckDailyMessageLimit(tenantID, funnel.SessionPhone); limitErr != nil {
			slog.Warn("funnels: daily limit reached, skipping auto-advance send", "funnel_id", funnel.ID, "error", limitErr)
			return true
		}
		var contact models.Contact
		if database.DB.First(&contact, "id = ?", contactID).Error == nil {
			go func() {
				if err := sendStepViaSession(funnel.SessionPhone, tenantID, contact.PhoneNumber, nextStep, contact); err == nil {
					billing.IncrementDailyCount(tenantID, funnel.SessionPhone)
				}
			}()
		}
	}

	slog.Info("funnels: contact auto-advanced",
		"contact_id", contactID, "from_step", fromStepID, "to_step", nextStep.Name, "funnel", funnel.Name)
	return true
}

// ── helpers ───────────────────────────────────────────────────────────────────

func personalize(template string, contact models.Contact) string {
	name := contact.Name
	if name == "" {
		name = contact.PushName
	}
	if name == "" {
		name = contact.PhoneNumber
	}
	return strings.ReplaceAll(template, "{name}", name)
}

func sendViaSession(sessionPhone string, tenantID uuid.UUID, to, text string) error {
	var sess models.WhatsAppSession
	// Order by created_at DESC so the most recently connected session wins when
	// there are stale CONNECTED rows from a previous registration of the same phone.
	if err := database.DB.Where("tenant_id = ? AND phone = ? AND status = 'CONNECTED'", tenantID, sessionPhone).
		Order("created_at DESC").First(&sess).Error; err != nil {
		return fmt.Errorf("session not connected")
	}
	// Use SendText which has a built-in fallback to any live client when the
	// stored session ID is no longer loaded in the manager (e.g. stale rows).
	_, err := session.Mgr.SendText(sess.ID.String(), to, text)
	return err
}

// sendStepViaSession sends a funnel step to a contact — an image-with-caption
// when the step has media, otherwise a plain text message. The step message is
// personalized ({name}) before sending.
func sendStepViaSession(sessionPhone string, tenantID uuid.UUID, to string, step models.FunnelStep, contact models.Contact) error {
	msg := step.Message
	var variants []string
	if json.Unmarshal([]byte(step.Variants), &variants) == nil && len(variants) > 0 {
		msg = variants[rand.Intn(len(variants))]
	}
	caption := personalize(msg, contact)
	if len(step.MediaPayload) > 0 {
		var sess models.WhatsAppSession
		if err := database.DB.Where("tenant_id = ? AND phone = ? AND status = 'CONNECTED'", tenantID, sessionPhone).
			Order("created_at DESC").First(&sess).Error; err != nil {
			return fmt.Errorf("session not connected")
		}
		_, _, _, err := session.Mgr.SendMedia(sess.ID.String(), to, step.MediaPayload, step.MediaMime, step.MediaName, caption)
		return err
	}
	return sendViaSession(sessionPhone, tenantID, to, caption)
}

// decodeMedia decodes an optional base64 image (raw or data URL) into bytes,
// returning the bytes and resolved MIME type. Enforces a 16 MB cap.
func decodeMedia(b64, mime string) ([]byte, string, error) {
	if b64 == "" {
		return nil, "", nil
	}
	if strings.HasPrefix(b64, "data:") {
		if comma := strings.IndexByte(b64, ','); comma != -1 {
			header := b64[5:comma]
			if semi := strings.IndexByte(header, ';'); semi != -1 {
				if mime == "" {
					mime = header[:semi]
				}
			}
			b64 = b64[comma+1:]
		}
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "", fmt.Errorf("invalid media encoding")
	}
	if len(data) > 16<<20 {
		return nil, "", fmt.Errorf("media too large (max 16 MB)")
	}
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	if mime == "application/ogg" {
		mime = "audio/ogg; codecs=opus"
	}
	return data, mime, nil
}

// ── Timeout Worker ───────────────────────────────────────────────────────────

// StartTimeoutWorker runs a background loop every 5 minutes that handles
// funnel contacts whose reply window has expired.
func StartTimeoutWorker(ctx context.Context) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("funnels: PANIC recovered in timeout worker", "panic", r)
			}
		}()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				processTimeouts()
			}
		}
	}()
}

func processTimeouts() {
	const batchSize = 500
	var lastID uuid.UUID

	for {
		var fcs []models.FunnelContact
		q := database.DB.Preload("Funnel").Preload("CurrentStep").
			Where("status = 'ACTIVE'")
		if lastID != uuid.Nil {
			q = q.Where("id > ?", lastID)
		}
		q.Order("id ASC").Limit(batchSize).Find(&fcs)
		if len(fcs) == 0 {
			break
		}
		lastID = fcs[len(fcs)-1].ID

		for _, fc := range fcs {
			if fc.Funnel.TimeoutAction == models.FunnelTimeoutNone || fc.Funnel.TimeoutAction == "" {
				continue
			}
			elapsed := time.Since(fc.LastMovedAt)
			windowDuration := time.Duration(fc.Funnel.ReplyWindowHours) * time.Hour
			if elapsed <= windowDuration {
				continue
			}

			switch fc.Funnel.TimeoutAction {
			case models.FunnelTimeoutAutoDrop:
				database.DB.Model(&fc).Updates(map[string]interface{}{
					"status": models.FunnelContactDropped,
				})
				history := models.FunnelContactHistory{
					FunnelID:   fc.FunnelID,
					ContactID:  fc.ContactID,
					FromStepID: &fc.CurrentStepID,
					ToStepID:   fc.CurrentStepID,
					Trigger:    "AUTO_TIMEOUT",
				}
				database.DB.Create(&history)
				slog.Info("funnels: auto-dropped contact (timeout)", "contact_id", fc.ContactID, "funnel", fc.Funnel.Name)

			case models.FunnelTimeoutFollowUp:
				if fc.Funnel.FollowUpMessage == "" {
					continue
				}
				// Check daily limit before sending follow-up
				if limitErr := billing.CheckDailyMessageLimit(fc.Funnel.TenantID, fc.Funnel.SessionPhone); limitErr != nil {
					slog.Warn("funnels: daily limit reached, skipping follow-up", "funnel_id", fc.FunnelID, "error", limitErr)
					continue
				}
				var contact models.Contact
				if database.DB.First(&contact, "id = ?", fc.ContactID).Error != nil {
					continue
				}
				msg := personalize(fc.Funnel.FollowUpMessage, contact)
				go func() {
					if err := sendViaSession(fc.Funnel.SessionPhone, fc.Funnel.TenantID, contact.PhoneNumber, msg); err == nil {
						billing.IncrementDailyCount(fc.Funnel.TenantID, fc.Funnel.SessionPhone)
					}
				}()
				database.DB.Model(&fc).Update("last_moved_at", time.Now())
				history := models.FunnelContactHistory{
					FunnelID:   fc.FunnelID,
					ContactID:  fc.ContactID,
					FromStepID: &fc.CurrentStepID,
					ToStepID:   fc.CurrentStepID,
					Trigger:    "AUTO_FOLLOWUP",
				}
				database.DB.Create(&history)
				slog.Info("funnels: sent follow-up", "contact_id", fc.ContactID, "funnel", fc.Funnel.Name)
			}
		}
	}
}
