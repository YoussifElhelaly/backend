package billing

import (
	"fmt"
	"log"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type PlanLimits struct {
	Sessions     int // -1 = unlimited
	MessagesDay  int // -1 = unlimited
	Agents       int // -1 = unlimited
	Flows        int // -1 = unlimited (active flows)
	Funnels      int // -1 = unlimited (active funnels)
	QuickReplies int // -1 = unlimited (total)
	Campaigns    int // -1 = unlimited (total non-deleted)
	PriceEGP     float64
	Label        string
}

var PlanDefs = map[models.Plan]PlanLimits{
	models.PlanStarter: {Sessions: 1, MessagesDay: 500, Agents: 2, Flows: 2, Funnels: 1, QuickReplies: 10, Campaigns: 5, PriceEGP: 599, Label: "Starter"},
	models.PlanGrowth:  {Sessions: 5, MessagesDay: 5000, Agents: 10, Flows: 10, Funnels: 5, QuickReplies: 50, Campaigns: -1, PriceEGP: 1499, Label: "Growth"},
	models.PlanScale:   {Sessions: 20, MessagesDay: -1, Agents: -1, Flows: -1, Funnels: -1, QuickReplies: -1, Campaigns: -1, PriceEGP: 2999, Label: "Scale"},
}

func GetLimits(plan models.Plan) PlanLimits {
	// Always prefer DB (admin can edit prices/limits via dashboard)
	var planDef models.PlanDef
	if err := database.DB.Where("name = ? AND is_active = true", plan).First(&planDef).Error; err == nil {
		return PlanLimits{
			Sessions:     planDef.Sessions,
			MessagesDay:  planDef.MessagesDay,
			Agents:       planDef.Agents,
			Flows:        planDef.Flows,
			Funnels:      planDef.Funnels,
			QuickReplies: planDef.QuickReplies,
			Campaigns:    planDef.Campaigns,
			PriceEGP:     planDef.PriceEGP,
			Label:        planDef.Label,
		}
	}
	// Fallback to hardcoded for safety
	if l, ok := PlanDefs[plan]; ok {
		return l
	}
	return PlanDefs[models.PlanStarter]
}

func CheckSessionLimit(tenantID uuid.UUID) error {
	var tenant models.Tenant
	if err := database.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil
	}

	limits := GetLimits(tenant.Plan)
	if limits.Sessions == -1 {
		return nil
	}

	var count int64
	database.DB.Model(&models.WhatsAppSession{}).
		Where("tenant_id = ? AND status != ?", tenantID, models.StatusBanned).
		Count(&count)

	if int(count) >= limits.Sessions {
		return fmt.Errorf("your %s plan allows up to %d WhatsApp number(s). Upgrade to add more", limits.Label, limits.Sessions)
	}
	return nil
}

// CheckDailyMessageLimit returns an error if the session has reached the
// tenant-configured daily outbound message limit (DailyMessageLimit).
// A value of 0 means unlimited.
func CheckDailyMessageLimit(tenantID uuid.UUID, sessionPhone string) error {
	var tenant models.Tenant
	if err := database.DB.Select("daily_message_limit").First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil
	}

	if tenant.DailyMessageLimit <= 0 {
		return nil // unlimited
	}

	var sess models.WhatsAppSession
	if err := database.DB.Select("daily_count").
		Where("tenant_id = ? AND phone = ?", tenantID, sessionPhone).
		First(&sess).Error; err != nil {
		return nil
	}

	if sess.DailyCount >= tenant.DailyMessageLimit {
		return fmt.Errorf("daily message limit reached (%d/%d messages). Contact admin to increase the limit",
			sess.DailyCount, tenant.DailyMessageLimit)
	}

	return nil
}

// GetDailyUsage returns the current daily usage (used, limit) for a session.
// limit = 0 means unlimited.
func GetDailyUsage(tenantID uuid.UUID, sessionPhone string) (used int, limit int) {
	var tenant models.Tenant
	if err := database.DB.Select("daily_message_limit").First(&tenant, "id = ?", tenantID).Error; err != nil {
		return 0, 0
	}

	var sess models.WhatsAppSession
	if err := database.DB.Select("daily_count").
		Where("tenant_id = ? AND phone = ?", tenantID, sessionPhone).
		First(&sess).Error; err != nil {
		return 0, tenant.DailyMessageLimit
	}

	return sess.DailyCount, tenant.DailyMessageLimit
}

// CheckCampaignLimitAtCreation validates that the number of selected contacts
// does not exceed the remaining daily quota for the session.
func CheckCampaignLimitAtCreation(tenantID uuid.UUID, sessionPhone string, contactCount int) error {
	var tenant models.Tenant
	if err := database.DB.Select("daily_message_limit").First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil
	}

	if tenant.DailyMessageLimit <= 0 {
		return nil
	}

	var sess models.WhatsAppSession
	if err := database.DB.Select("daily_count").
		Where("tenant_id = ? AND phone = ?", tenantID, sessionPhone).
		First(&sess).Error; err != nil {
		return nil
	}

	remaining := tenant.DailyMessageLimit - sess.DailyCount
	if remaining <= 0 {
		return fmt.Errorf("daily limit already reached (%d/%d). No more messages can be sent today",
			sess.DailyCount, tenant.DailyMessageLimit)
	}

	if contactCount > remaining {
		return fmt.Errorf("cannot select %d contacts — only %d messages remaining today for this session (%d/%d used)",
			contactCount, remaining, sess.DailyCount, tenant.DailyMessageLimit)
	}

	return nil
}

// IncrementDailyCount adds 1 to the daily_count of the session identified by
// (tenantID, sessionPhone). Called after every successful outbound message.
func IncrementDailyCount(tenantID uuid.UUID, sessionPhone string) {
	if err := database.DB.Model(&models.WhatsAppSession{}).
		Where("tenant_id = ? AND phone = ?", tenantID, sessionPhone).
		Update("daily_count", gorm.Expr("daily_count + 1")).Error; err != nil {
		log.Printf("billing: failed to increment daily_count for %s: %v", sessionPhone, err)
	}
}

// ResetDailyCounts zeroes daily_count on all sessions. Call this once per day.
func ResetDailyCounts() {
	database.DB.Model(&models.WhatsAppSession{}).
		Where("daily_count > 0").
		Update("daily_count", 0)
	log.Println("billing: daily message counts reset")
}

func CheckAgentLimit(tenantID uuid.UUID) error {
	var tenant models.Tenant
	if err := database.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil
	}

	limits := GetLimits(tenant.Plan)
	if limits.Agents == -1 {
		return nil
	}

	var count int64
	database.DB.Model(&models.User{}).
		Where("tenant_id = ?", tenantID).
		Count(&count)

	if int(count) >= limits.Agents {
		return fmt.Errorf("your %s plan allows up to %d team members. Upgrade to add more", limits.Label, limits.Agents)
	}
	return nil
}

// CheckFlowLimit checks if the tenant can create another active flow.
func CheckFlowLimit(tenantID uuid.UUID) error {
	var tenant models.Tenant
	if err := database.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil
	}

	limits := GetLimits(tenant.Plan)
	if limits.Flows == -1 {
		return nil
	}

	var count int64
	database.DB.Model(&models.Flow{}).
		Where("tenant_id = ? AND is_active = true", tenantID).
		Count(&count)

	if int(count) >= limits.Flows {
		return fmt.Errorf("your %s plan allows up to %d active automation flow(s). Upgrade or disable an existing flow to add more", limits.Label, limits.Flows)
	}
	return nil
}

// CheckFunnelLimit checks if the tenant can create another active funnel.
func CheckFunnelLimit(tenantID uuid.UUID) error {
	var tenant models.Tenant
	if err := database.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil
	}

	limits := GetLimits(tenant.Plan)
	if limits.Funnels == -1 {
		return nil
	}

	var count int64
	database.DB.Model(&models.Funnel{}).
		Where("tenant_id = ? AND status = ?", tenantID, "ACTIVE").
		Count(&count)

	if int(count) >= limits.Funnels {
		return fmt.Errorf("your %s plan allows up to %d active funnel(s). Upgrade or pause an existing funnel to add more", limits.Label, limits.Funnels)
	}
	return nil
}

// CheckQuickReplyLimit checks if the tenant can create another quick reply.
func CheckQuickReplyLimit(tenantID uuid.UUID) error {
	var tenant models.Tenant
	if err := database.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil
	}

	limits := GetLimits(tenant.Plan)
	if limits.QuickReplies == -1 {
		return nil
	}

	var count int64
	database.DB.Model(&models.QuickReply{}).
		Where("tenant_id = ?", tenantID).
		Count(&count)

	if int(count) >= limits.QuickReplies {
		return fmt.Errorf("your %s plan allows up to %d quick reply template(s). Upgrade or delete an existing one to add more", limits.Label, limits.QuickReplies)
	}
	return nil
}

// CheckCampaignLimit checks if the tenant can create another campaign.
func CheckCampaignLimit(tenantID uuid.UUID) error {
	var tenant models.Tenant
	if err := database.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil
	}

	limits := GetLimits(tenant.Plan)
	if limits.Campaigns == -1 {
		return nil
	}

	var count int64
	database.DB.Model(&models.Campaign{}).
		Where("tenant_id = ?", tenantID).
		Count(&count)

	if int(count) >= limits.Campaigns {
		return fmt.Errorf("your %s plan allows up to %d campaign(s). Upgrade or delete an existing campaign to add more", limits.Label, limits.Campaigns)
	}
	return nil
}
