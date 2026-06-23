package funnels

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
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
	Name    string `json:"name" binding:"required"`
	Type    string `json:"type" binding:"required"`
	Message string `json:"message"`
}

type CreateFunnelInput struct {
	Name             string      `json:"name" binding:"required"`
	SessionPhone     string      `json:"session_phone" binding:"required"`
	Description      string      `json:"description"`
	ReplyWindowHours int         `json:"reply_window_hours"`
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
	}
	if err := database.DB.Create(&funnel).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create funnel"})
		return
	}

	for i, s := range input.Steps {
		stepType := models.FunnelStepType(s.Type)
		step := models.FunnelStep{
			FunnelID: funnel.ID,
			Order:    i + 1,
			Name:     s.Name,
			Type:     stepType,
			Message:  s.Message,
		}
		database.DB.Create(&step)
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
		Name             string `json:"name"`
		Description      string `json:"description"`
		ReplyWindowHours int    `json:"reply_window_hours"`
	}
	c.ShouldBindJSON(&input)
	if input.Name != "" {
		funnel.Name = input.Name
	}
	if input.Description != "" {
		funnel.Description = input.Description
	}
	if input.ReplyWindowHours > 0 {
		funnel.ReplyWindowHours = input.ReplyWindowHours
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

	step := models.FunnelStep{
		FunnelID: funnel.ID,
		Order:    maxOrder + 1,
		Name:     input.Name,
		Type:     models.FunnelStepType(input.Type),
		Message:  input.Message,
	}
	database.DB.Create(&step)
	c.JSON(http.StatusCreated, step)
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

	var fcs []models.FunnelContact
	database.DB.Preload("Contact.Tags").Preload("CurrentStep").
		Where("funnel_id = ?", funnel.ID).Find(&fcs)

	stepMap := map[uuid.UUID]*PipelineStep{}
	result := make([]PipelineStep, 0, len(funnel.Steps))
	for _, s := range funnel.Steps {
		ps := PipelineStep{FunnelStep: s, Contacts: []models.FunnelContact{}}
		result = append(result, ps)
		stepMap[s.ID] = &result[len(result)-1]
	}

	var total, active, converted, dropped int
	for _, fc := range fcs {
		total++
		switch fc.Status {
		case models.FunnelContactActive:
			active++
		case models.FunnelContactConverted:
			converted++
		case models.FunnelContactDropped:
			dropped++
		}
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
	})
}

// ── Launch ───────────────────────────────────────────────────────────────────

type LaunchInput struct {
	ContactIDs []uuid.UUID `json:"contact_ids"`
	TagIDs     []uuid.UUID `json:"tag_ids"`
}

func launchFunnel(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
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

	// Get WhatsApp client
	var sess models.WhatsAppSession
	database.DB.Where("tenant_id = ? AND phone = ? AND status = 'CONNECTED'", tenantID, funnel.SessionPhone).First(&sess)
	var client interface{ SendMessage(context.Context, types.JID, *waE2E.Message, ...interface{}) (interface{}, error) }
	if sess.ID != uuid.Nil {
		client = nil // we'll use session.Mgr below
	}
	_ = client

	queued := 0
	skipped := 0
	_ = userID

	go func() {
		for contactID := range contactMap {
			// Skip if already active in this funnel
			var existing models.FunnelContact
			if database.DB.Where("funnel_id = ? AND contact_id = ? AND status = 'ACTIVE'", funnel.ID, contactID).First(&existing).Error == nil {
				skipped++
				continue
			}

			var contact models.Contact
			if err := database.DB.First(&contact, "id = ?", contactID).Error; err != nil {
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

			// Send entry message if present
			if entryStep.Message != "" {
				text := personalize(entryStep.Message, contact)
				err := sendViaSession(funnel.SessionPhone, tenantID, contact.PhoneNumber, text)
				sentAt := time.Now()
				if err != nil {
					log.Printf("funnels: failed to send to %s: %v", contact.PhoneNumber, err)
					database.DB.Model(&cc).Updates(map[string]interface{}{
						"status":    models.CampaignContactFailed,
						"error_msg": err.Error(),
					})
				} else {
					database.DB.Model(&cc).Updates(map[string]interface{}{
						"status":  models.CampaignContactSent,
						"sent_at": &sentAt,
					})
					database.DB.Model(&campaign).Update("sent_count", campaign.SentCount+1)
					campaign.SentCount++
				}
			}

			queued++
			// Anti-spam jitter
			time.Sleep(time.Duration(3+queued%5) * time.Second)
		}

		completedAt := time.Now()
		database.DB.Model(&campaign).Updates(map[string]interface{}{
			"status":       models.CampaignStatusCompleted,
			"completed_at": &completedAt,
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
		"message":     "Funnel launched",
		"campaign_id": campaign.ID,
		"queued":      len(contactMap),
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
	if toStep.Message != "" {
		var contact models.Contact
		if database.DB.First(&contact, "id = ?", input.ContactID).Error == nil {
			go sendViaSession(funnel.SessionPhone, tenantID, contact.PhoneNumber, personalize(toStep.Message, contact))
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

	database.DB.Model(&models.FunnelContact{}).
		Where("funnel_id = ? AND contact_id = ?", funnel.ID, cid).
		Update("status", input.Status)

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

func HandleInboundReply(contactID uuid.UUID, tenantID uuid.UUID) {
	var fc models.FunnelContact
	if err := database.DB.Preload("Funnel").
		Where("contact_id = ? AND status = 'ACTIVE'", contactID).
		First(&fc).Error; err != nil {
		return // contact not in any active funnel
	}

	var funnel models.Funnel
	if err := database.DB.Where("id = ? AND tenant_id = ?", fc.FunnelID, tenantID).First(&funnel).Error; err != nil {
		return
	}
	if funnel.Status != models.FunnelStatusActive {
		return
	}

	// Check reply window
	elapsed := time.Since(fc.LastMovedAt)
	windowDuration := time.Duration(funnel.ReplyWindowHours) * time.Hour
	if elapsed > windowDuration {
		return
	}

	// Get current step
	var currentStep models.FunnelStep
	if err := database.DB.First(&currentStep, "id = ?", fc.CurrentStepID).Error; err != nil {
		return
	}

	// Only auto-advance on ENTRY or REPLY_TRIGGER steps
	if currentStep.Type != models.FunnelStepEntry && currentStep.Type != models.FunnelStepReplyTrigger {
		return
	}

	// Get next step
	var nextStep models.FunnelStep
	if err := database.DB.Where("funnel_id = ? AND \"order\" = ?", funnel.ID, currentStep.Order+1).
		First(&nextStep).Error; err != nil {
		return // no next step
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
	if nextStep.Message != "" {
		var contact models.Contact
		if database.DB.First(&contact, "id = ?", contactID).Error == nil {
			go sendViaSession(funnel.SessionPhone, tenantID, contact.PhoneNumber, personalize(nextStep.Message, contact))
		}
	}

	log.Printf("funnels: contact %s auto-advanced to step %s", contactID, nextStep.Name)
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
	if err := database.DB.Where("tenant_id = ? AND phone = ? AND status = 'CONNECTED'", tenantID, sessionPhone).
		First(&sess).Error; err != nil {
		return fmt.Errorf("session not connected")
	}
	client := session.Mgr.GetClient(sess.ID.String())
	if client == nil {
		return fmt.Errorf("whatsapp client not found")
	}
	jid := types.NewJID(to, types.DefaultUserServer)
	msg := &waE2E.Message{Conversation: proto.String(text)}
	_, err := client.SendMessage(context.Background(), jid, msg)
	return err
}
