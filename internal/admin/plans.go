package admin

import (
	"net/http"
	"strconv"
	"time"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/features"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Plan Definitions CRUD ──────────────────────────────────────────────────

func handleListPlans(c *gin.Context) {
	var plans []models.PlanDef
	if err := database.DB.Order("price_usd asc").Find(&plans).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, plans)
}

type CreatePlanRequest struct {
	Name             string   `json:"name"          binding:"required"`
	Label            string   `json:"label"         binding:"required"`
	PriceUSD         float64  `json:"price_usd"     binding:"required,min=0"`
	OriginalPriceUSD float64  `json:"original_price_usd"`
	Period           string   `json:"period"`
	IntervalCount    int      `json:"interval_count"`
	Desc             string   `json:"desc"`
	Badge            string   `json:"badge"`
	CTA              string   `json:"cta"`
	SortOrder        int      `json:"sort_order"`
	Sessions         int      `json:"sessions"      binding:"required,min=1"`
	MessagesDay      int      `json:"messages_day"  binding:"required,min=-1"`
	Agents           int      `json:"agents"        binding:"required,min=-1"`
	Flows            int      `json:"flows"`
	Funnels          int      `json:"funnels"`
	QuickReplies     int      `json:"quick_replies"`
	Campaigns        int      `json:"campaigns"`
	Features         []string `json:"features"`
}

func handleCreatePlan(c *gin.Context) {
	var req CreatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check name uniqueness
	var existing models.PlanDef
	if err := database.DB.Where("name = ?", req.Name).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "plan name already exists"})
		return
	}

	period := req.Period
	if period == "" {
		period = "mo"
	}
	cta := req.CTA
	if cta == "" {
		cta = "Start free"
	}

	intervalCount := req.IntervalCount
	if intervalCount < 1 {
		intervalCount = 1
	}

	flows := req.Flows
	if flows == 0 {
		flows = -1
	}
	funnels := req.Funnels
	if funnels == 0 {
		funnels = -1
	}
	quickReplies := req.QuickReplies
	if quickReplies == 0 {
		quickReplies = -1
	}
	campaigns := req.Campaigns
	if campaigns == 0 {
		campaigns = -1
	}

	plan := models.PlanDef{
		Name:             req.Name,
		Label:            req.Label,
		PriceUSD:         req.PriceUSD,
		OriginalPriceUSD: req.OriginalPriceUSD,
		Period:           period,
		IntervalCount:    intervalCount,
		Desc:             req.Desc,
		Badge:            req.Badge,
		CTA:              cta,
		SortOrder:        req.SortOrder,
		Sessions:         req.Sessions,
		MessagesDay:      req.MessagesDay,
		Agents:           req.Agents,
		Flows:            flows,
		Funnels:          funnels,
		QuickReplies:     quickReplies,
		Campaigns:        campaigns,
		Features:         features.ToJSON(req.Features),
		IsCustom:         true,
		IsActive:         true,
	}
	if err := database.DB.Create(&plan).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Trigger PayPal plan creation for the new custom plan in the background
	go billing.SetupPayPalPlans()

	c.JSON(http.StatusCreated, plan)
}

type UpdatePlanRequest struct {
	Label            *string   `json:"label"`
	PriceUSD         *float64  `json:"price_usd"`
	OriginalPriceUSD *float64  `json:"original_price_usd"`
	Period           *string   `json:"period"`
	IntervalCount    *int      `json:"interval_count"`
	Desc             *string   `json:"desc"`
	Badge            *string   `json:"badge"`
	CTA              *string   `json:"cta"`
	SortOrder        *int      `json:"sort_order"`
	Sessions         *int      `json:"sessions"`
	MessagesDay      *int      `json:"messages_day"`
	Agents           *int      `json:"agents"`
	Flows            *int      `json:"flows"`
	Funnels          *int      `json:"funnels"`
	QuickReplies     *int      `json:"quick_replies"`
	Campaigns        *int      `json:"campaigns"`
	IsActive         *bool     `json:"is_active"`
	Features         *[]string `json:"features"`
}

func handleUpdatePlan(c *gin.Context) {
	id := c.Param("id")
	var plan models.PlanDef
	if err := database.DB.Where("id = ?", id).First(&plan).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
		return
	}

	var req UpdatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{}
	if req.Label != nil {
		updates["label"] = *req.Label
	}
	if req.PriceUSD != nil {
		updates["price_usd"] = *req.PriceUSD
	}
	if req.OriginalPriceUSD != nil {
		updates["original_price_usd"] = *req.OriginalPriceUSD
	}
	if req.Period != nil {
		updates["period"] = *req.Period
	}
	if req.IntervalCount != nil {
		updates["interval_count"] = *req.IntervalCount
	}
	if req.Desc != nil {
		updates["desc"] = *req.Desc
	}
	if req.Badge != nil {
		updates["badge"] = *req.Badge
	}
	if req.CTA != nil {
		updates["cta"] = *req.CTA
	}
	if req.SortOrder != nil {
		updates["sort_order"] = *req.SortOrder
	}
	if req.Sessions != nil {
		updates["sessions"] = *req.Sessions
	}
	if req.MessagesDay != nil {
		updates["messages_day"] = *req.MessagesDay
	}
	if req.Agents != nil {
		updates["agents"] = *req.Agents
	}
	if req.Flows != nil {
		updates["flows"] = *req.Flows
	}
	if req.Funnels != nil {
		updates["funnels"] = *req.Funnels
	}
	if req.QuickReplies != nil {
		updates["quick_replies"] = *req.QuickReplies
	}
	if req.Campaigns != nil {
		updates["campaigns"] = *req.Campaigns
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}
	if req.Features != nil {
		updates["features"] = features.ToJSON(*req.Features)
	}

	if len(updates) > 0 {
		if err := database.DB.Model(&plan).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		
		// Synchronize plans with PayPal in the background so changes take effect immediately
		go billing.SetupPayPalPlans()
	}

	database.DB.First(&plan, "id = ?", id)
	c.JSON(http.StatusOK, plan)
}

func handleDeletePlan(c *gin.Context) {
	id := c.Param("id")
	var plan models.PlanDef
	if err := database.DB.Where("id = ?", id).First(&plan).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
		return
	}

	// Don't allow deleting built-in plans
	if !plan.IsCustom {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete built-in plan"})
		return
	}

	// Check if any tenants use this plan
	var count int64
	database.DB.Model(&models.Tenant{}).Where("plan = ?", plan.Name).Count(&count)
	if count > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan is in use by tenants, cannot delete"})
		return
	}

	database.DB.Delete(&plan)
	c.JSON(http.StatusOK, gin.H{"message": "plan deleted"})
}

// ─── Subscription Management ─────────────────────────────────────────────────

func handleListAllSubscriptions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	var total int64
	database.DB.Model(&models.Subscription{}).Count(&total)

	var subs []models.Subscription
	if err := database.DB.Order("created_at desc").Limit(limit).Offset(offset).Find(&subs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type SubRow struct {
		models.Subscription
		TenantName string `json:"tenant_name"`
	}

	rows := make([]SubRow, 0, len(subs))
	tenantCache := map[uuid.UUID]string{}
	for _, s := range subs {
		row := SubRow{Subscription: s}
		if name, ok := tenantCache[s.TenantID]; ok {
			row.TenantName = name
		} else {
			var t models.Tenant
			if err := database.DB.Select("name").Where("id = ?", s.TenantID).First(&t).Error; err == nil {
				tenantCache[s.TenantID] = t.Name
				row.TenantName = t.Name
			}
		}
		rows = append(rows, row)
	}

	c.JSON(http.StatusOK, gin.H{
		"subscriptions": rows,
		"total":         total,
		"page":          page,
		"limit":         limit,
	})
}

type UpdateSubscriptionRequest struct {
	Status   string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
	Amount   *float64   `json:"amount"`
}

func handleUpdateSubscription(c *gin.Context) {
	id := c.Param("id")
	var sub models.Subscription
	if err := database.DB.Where("id = ?", id).First(&sub).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
		return
	}

	var req UpdateSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{}
	if req.Status != "" {
		updates["status"] = req.Status
		if req.Status == "PAID" || req.Status == "ACTIVE" {
			now := time.Now()
			updates["paid_at"] = now
		}
	}
	if req.ExpiresAt != nil {
		updates["expires_at"] = req.ExpiresAt
	}
	if req.Amount != nil {
		updates["amount"] = *req.Amount
	}

	if len(updates) > 0 {
		if err := database.DB.Model(&sub).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	database.DB.First(&sub, "id = ?", id)
	c.JSON(http.StatusOK, sub)
}

func handleCancelSubscription(c *gin.Context) {
	id := c.Param("id")
	var sub models.Subscription
	if err := database.DB.Where("id = ?", id).First(&sub).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
		return
	}

	if sub.Status == models.SubStatusCancelled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "subscription already cancelled"})
		return
	}

	database.DB.Model(&sub).Updates(map[string]interface{}{
		"status": models.SubStatusCancelled,
	})

	// Also update tenant plan status
	database.DB.Model(&models.Tenant{}).Where("id = ?", sub.TenantID).Updates(map[string]interface{}{
		"paypal_sub_id": "",
	})

	c.JSON(http.StatusOK, gin.H{"message": "subscription cancelled", "status": "CANCELLED"})
}

func handleExtendSubscription(c *gin.Context) {
	id := c.Param("id")
	var sub models.Subscription
	if err := database.DB.Where("id = ?", id).First(&sub).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
		return
	}

	var req struct {
		Days int `json:"days" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	base := time.Now()
	if sub.ExpiresAt != nil && sub.ExpiresAt.After(time.Now()) {
		base = *sub.ExpiresAt
	}
	newExpiry := base.AddDate(0, 0, req.Days)

	database.DB.Model(&sub).Update("expires_at", newExpiry)

	// Also update tenant
	database.DB.Model(&models.Tenant{}).Where("id = ?", sub.TenantID).Update("plan_expires_at", newExpiry)

	c.JSON(http.StatusOK, gin.H{"message": "subscription extended", "expires_at": newExpiry})
}
