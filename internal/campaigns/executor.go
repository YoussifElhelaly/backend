package campaigns

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
)

var (
	running sync.Map // campaign uuid → context.CancelFunc
)

// SendText is wired from session.Mgr — allows campaigns to send messages
var SendText func(sessionPhone, tenantID, to, text string) error

// SendMedia sends an image/media message with an optional caption.
var SendMedia func(sessionPhone, tenantID, to string, data []byte, mime, filename, caption string) error

func init() {
	SendText = defaultSendText
	SendMedia = defaultSendMedia
}

func defaultSendMedia(sessionPhone, tenantID, to string, data []byte, mime, filename, caption string) error {
	tid, err := uuid.Parse(tenantID)
	if err != nil {
		return err
	}
	var sess models.WhatsAppSession
	if err := database.DB.Where("tenant_id = ? AND phone = ? AND status = 'CONNECTED'", tid, sessionPhone).First(&sess).Error; err != nil {
		return fmt.Errorf("session not connected")
	}
	_, _, _, err = session.Mgr.SendMedia(sess.ID.String(), to, data, mime, filename, caption)
	return err
}

func defaultSendText(sessionPhone, tenantID, to, text string) error {
	var sess models.WhatsAppSession
	tid, err := uuid.Parse(tenantID)
	if err != nil {
		return err
	}
	if err := database.DB.Where("tenant_id = ? AND phone = ? AND status = 'CONNECTED'", tid, sessionPhone).First(&sess).Error; err != nil {
		return fmt.Errorf("session not connected")
	}
	_, err = session.Mgr.SendTextWithTyping(sess.ID.String(), to, text)
	return err
}

// pickVariant returns a round-robin variant from variantsJSON, falling back to fallback.
// idx is the contact index used for round-robin rotation.
func pickVariant(variantsJSON, fallback string, idx int) string {
	var variants []string
	if err := json.Unmarshal([]byte(variantsJSON), &variants); err != nil || len(variants) == 0 {
		return fallback
	}
	return variants[idx%len(variants)]
}

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

// Launch starts sending a campaign in the background.
func Launch(campaignID uuid.UUID) {
	ctx, cancel := context.WithCancel(context.Background())
	running.Store(campaignID, cancel)
	go run(ctx, campaignID)
}

// Pause cancels a running campaign goroutine and marks it PAUSED.
func Pause(campaignID uuid.UUID) {
	if cancel, ok := running.Load(campaignID); ok {
		cancel.(context.CancelFunc)()
	}
	database.DB.Model(&models.Campaign{}).Where("id = ?", campaignID).
		Updates(map[string]interface{}{"status": models.CampaignStatusPaused})
}

// ResumeInterrupted picks up RUNNING campaigns left over from a server crash.
func ResumeInterrupted() {
	var campaigns []models.Campaign
	database.DB.Where("status = ?", models.CampaignStatusRunning).Find(&campaigns)
	for _, c := range campaigns {
		slog.Info("campaigns: resuming interrupted campaign", "campaign_id", c.ID)
		Launch(c.ID)
	}
}

func run(ctx context.Context, campaignID uuid.UUID) {
	defer running.Delete(campaignID)
	defer func() {
		if r := recover(); r != nil {
			slog.Error("campaigns: PANIC recovered", "campaign_id", campaignID, "panic", r)
		}
	}()

	now := time.Now()
	database.DB.Model(&models.Campaign{}).Where("id = ?", campaignID).
		Updates(map[string]interface{}{
			"status":     models.CampaignStatusRunning,
			"started_at": &now,
		})

	var campaign models.Campaign
		if err := database.DB.First(&campaign, "id = ?", campaignID).Error; err != nil {
			slog.Error("campaigns: campaign not found", "campaign_id", campaignID, "error", err)
			return
		}

	var contacts []models.CampaignContact
	database.DB.Preload("Contact").
		Where("campaign_id = ? AND status = ?", campaignID, models.CampaignContactPending).
		Find(&contacts)

	// Load per-tenant delay settings once before the loop
	var tenantSettings models.Tenant
	database.DB.Select("campaign_delay_min, campaign_delay_max").
		Where("id = ?", campaign.TenantID).First(&tenantSettings)
	dMin := tenantSettings.CampaignDelayMin
	dMax := tenantSettings.CampaignDelayMax
	if dMin < 1 {
		dMin = 3
	}
	if dMax < dMin {
		dMax = dMin + 5
	}
	spread := dMax - dMin + 1

	for idx, cc := range contacts {
		select {
		case <-ctx.Done():
			log.Printf("campaigns: campaign %s paused/cancelled", campaignID)
			return
		default:
		}

		// Abort campaign if the plan daily limit or tenant CAP has been reached
		if limitErr := billing.CheckDailyMessageLimit(campaign.TenantID, campaign.SessionPhone); limitErr != nil {
			slog.Warn("campaigns: daily limit reached, pausing", "campaign_id", campaignID, "error", limitErr)
			database.DB.Model(&models.Campaign{}).Where("id = ?", campaignID).
				Updates(map[string]interface{}{
					"status":    models.CampaignStatusPaused,
					"error_msg": limitErr.Error(),
				})
			return
		}

		// Pick message: use variants (round-robin) if approved, else original
		msgTemplate := pickVariant(campaign.Variants, campaign.Message, idx)
		text := personalize(msgTemplate, cc.Contact)
		var err error
		if len(campaign.MediaPayload) > 0 {
			err = SendMedia(campaign.SessionPhone, campaign.TenantID.String(), cc.Contact.PhoneNumber, campaign.MediaPayload, campaign.MediaMime, campaign.MediaName, text)
		} else {
			err = SendText(campaign.SessionPhone, campaign.TenantID.String(), cc.Contact.PhoneNumber, text)
		}

		sentAt := time.Now()
		if err != nil {
			slog.Error("campaigns: send failed", "campaign_id", campaignID, "phone", cc.Contact.PhoneNumber, "error", err)
			database.DB.Model(&cc).Updates(map[string]interface{}{
				"status":    models.CampaignContactFailed,
				"error_msg": err.Error(),
			})
			database.DB.Model(&models.Campaign{}).Where("id = ?", campaignID).
				Update("failed_count", campaign.FailedCount+1)
			campaign.FailedCount++
		} else {
			billing.IncrementDailyCount(campaign.TenantID, campaign.SessionPhone)
			database.DB.Model(&cc).Updates(map[string]interface{}{
				"status":  models.CampaignContactSent,
				"sent_at": &sentAt,
			})
			database.DB.Model(&models.Campaign{}).Where("id = ?", campaignID).
				Update("sent_count", campaign.SentCount+1)
			campaign.SentCount++
		}

		jitter := time.Duration(dMin+rand.Intn(spread)) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter):
		}
	}

	completedAt := time.Now()
	database.DB.Model(&models.Campaign{}).Where("id = ?", campaignID).
		Updates(map[string]interface{}{
			"status":       models.CampaignStatusCompleted,
			"completed_at": &completedAt,
		})
	slog.Info("campaigns: campaign completed", "campaign_id", campaignID, "sent", campaign.SentCount, "failed", campaign.FailedCount)
}
