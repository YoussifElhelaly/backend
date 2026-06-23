package campaigns

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

var (
	running sync.Map // campaign uuid → context.CancelFunc
)

// SendText is wired from session.Mgr — allows campaigns to send messages
var SendText func(sessionPhone, tenantID, to, text string) error

func init() {
	SendText = defaultSendText
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
	client := session.Mgr.GetClient(sess.ID.String())
	if client == nil {
		return fmt.Errorf("whatsapp client not found")
	}
	jid := types.NewJID(to, types.DefaultUserServer)
	msg := &waE2E.Message{Conversation: proto.String(text)}
	_, sendErr := client.SendMessage(context.Background(), jid, msg)
	return sendErr
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
		log.Printf("campaigns: resuming interrupted campaign %s", c.ID)
		Launch(c.ID)
	}
}

func run(ctx context.Context, campaignID uuid.UUID) {
	defer running.Delete(campaignID)

	now := time.Now()
	database.DB.Model(&models.Campaign{}).Where("id = ?", campaignID).
		Updates(map[string]interface{}{
			"status":     models.CampaignStatusRunning,
			"started_at": &now,
		})

	var campaign models.Campaign
	if err := database.DB.First(&campaign, "id = ?", campaignID).Error; err != nil {
		log.Printf("campaigns: campaign %s not found: %v", campaignID, err)
		return
	}

	var contacts []models.CampaignContact
	database.DB.Preload("Contact").
		Where("campaign_id = ? AND status = ?", campaignID, models.CampaignContactPending).
		Find(&contacts)

	for _, cc := range contacts {
		select {
		case <-ctx.Done():
			log.Printf("campaigns: campaign %s paused/cancelled", campaignID)
			return
		default:
		}

		// Abort campaign if the daily message limit has been reached
		if limitErr := billing.CheckDailyMessageLimit(campaign.TenantID, campaign.SessionPhone); limitErr != nil {
			log.Printf("campaigns: campaign %s paused — daily limit: %v", campaignID, limitErr)
			database.DB.Model(&models.Campaign{}).Where("id = ?", campaignID).
				Updates(map[string]interface{}{
					"status":    models.CampaignStatusPaused,
					"error_msg": limitErr.Error(),
				})
			return
		}

		text := personalize(campaign.Message, cc.Contact)
		err := SendText(campaign.SessionPhone, campaign.TenantID.String(), cc.Contact.PhoneNumber, text)

		sentAt := time.Now()
		if err != nil {
			log.Printf("campaigns: failed to send to %s: %v", cc.Contact.PhoneNumber, err)
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

		// anti-spam jitter: 3-8 seconds
		jitter := time.Duration(3+rand.Intn(6)) * time.Second
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
	log.Printf("campaigns: campaign %s completed — sent:%d failed:%d", campaignID, campaign.SentCount, campaign.FailedCount)
}
