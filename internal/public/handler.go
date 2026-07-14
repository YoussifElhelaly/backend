package public

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
)

type PlanResponse struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Label            string   `json:"label"`
	PriceEGP         float64  `json:"price_egp"`
	OriginalPriceEGP float64  `json:"original_price_egp"`
	Price6moEGP      float64  `json:"price_6mo_egp"`
	Price12moEGP     float64  `json:"price_12mo_egp"`
	IntervalCount    int      `json:"interval_count"`
	PriceStr         string   `json:"price"`
	Period           string   `json:"period"`
	Desc             string   `json:"desc"`
	Badge            string   `json:"badge"`
	CTA              string   `json:"cta"`
	SortOrder        int      `json:"sort_order"`
	Sessions         int      `json:"sessions"`
	MessagesDay      int      `json:"messages_day"`
	Agents           int      `json:"agents"`
	Flows            int      `json:"flows"`
	Funnels          int      `json:"funnels"`
	QuickReplies     int      `json:"quick_replies"`
	Campaigns        int      `json:"campaigns"`
	Features         []string `json:"features"`
}

func listPlans(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	var plans []models.PlanDef
	if err := database.DB.
		Where("is_active = true AND is_custom = false").
		Order("sort_order ASC").
		Find(&plans).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	out := make([]PlanResponse, 0, len(plans))
	for _, p := range plans {
		var feats []string
		_ = json.Unmarshal([]byte(p.Features), &feats)

		out = append(out, PlanResponse{
			ID:               p.ID.String(),
			Name:             p.Name,
			Label:            p.Label,
			PriceEGP:         p.PriceEGP,
			OriginalPriceEGP: p.OriginalPriceEGP,
			Price6moEGP:      p.Price6moEGP,
			Price12moEGP:     p.Price12moEGP,
			IntervalCount:    p.IntervalCount,
			PriceStr:         fmt.Sprintf("EGP %.0f", p.PriceEGP),
			Period:           p.Period,
			Desc:             p.Desc,
			Badge:            p.Badge,
			CTA:              p.CTA,
			SortOrder:        p.SortOrder,
			Sessions:         p.Sessions,
			MessagesDay:      p.MessagesDay,
			Agents:           p.Agents,
			Flows:            p.Flows,
			Funnels:          p.Funnels,
			QuickReplies:     p.QuickReplies,
			Campaigns:        p.Campaigns,
			Features:         feats,
		})
	}

	c.JSON(http.StatusOK, out)
}

func submitLead(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	var body struct {
		Name  string `json:"name"  binding:"required"`
		Email string `json:"email" binding:"required"`
		Phone string `json:"phone"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name and email are required"})
		return
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	body.Name = strings.TrimSpace(body.Name)

	lead := models.Lead{
		Name:   body.Name,
		Email:  body.Email,
		Phone:  strings.TrimSpace(body.Phone),
		Source: "landing",
	}
	if err := database.DB.Create(&lead).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true})
}

func RegisterRoutes(r gin.IRouter) {
	pub := r.Group("/public")
	corsMiddleware := func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
	pub.Use(corsMiddleware)
	pub.GET("/plans", listPlans)
	pub.POST("/leads", submitLead)
}
