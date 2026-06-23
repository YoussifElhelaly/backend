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
	Sessions    int // -1 = unlimited
	MessagesDay int // -1 = unlimited
	Agents      int // -1 = unlimited
	PriceUSD    float64
	Label       string
}

var PlanDefs = map[models.Plan]PlanLimits{
	models.PlanStarter: {Sessions: 1, MessagesDay: 500, Agents: 2, PriceUSD: 19, Label: "Starter"},
	models.PlanGrowth:  {Sessions: 5, MessagesDay: 5000, Agents: 10, PriceUSD: 49, Label: "Growth"},
	models.PlanScale:   {Sessions: 20, MessagesDay: -1, Agents: -1, PriceUSD: 99, Label: "Scale"},
}

func GetLimits(plan models.Plan) PlanLimits {
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

// CheckDailyMessageLimit returns an error if the session has reached its plan's
// daily outbound message limit. sessionPhone identifies which WhatsApp session to check.
func CheckDailyMessageLimit(tenantID uuid.UUID, sessionPhone string) error {
	var tenant models.Tenant
	if err := database.DB.Select("plan").First(&tenant, "id = ?", tenantID).Error; err != nil {
		return nil
	}
	limits := GetLimits(tenant.Plan)
	if limits.MessagesDay == -1 {
		return nil // unlimited plan
	}

	var sess models.WhatsAppSession
	if err := database.DB.Select("daily_count").
		Where("tenant_id = ? AND phone = ?", tenantID, sessionPhone).
		First(&sess).Error; err != nil {
		return nil // session not found — let the send fail elsewhere
	}

	if sess.DailyCount >= limits.MessagesDay {
		return fmt.Errorf("daily message limit reached (%d/%d messages). Upgrade your plan to send more",
			sess.DailyCount, limits.MessagesDay)
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
