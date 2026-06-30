package public

import (
	"encoding/json"
	"fmt"
	"net/http"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
)

type PlanResponse struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Label            string   `json:"label"`
	PriceUSD         float64  `json:"price_usd"`
	OriginalPriceUSD float64  `json:"original_price_usd"`
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
			PriceUSD:         p.PriceUSD,
			OriginalPriceUSD: p.OriginalPriceUSD,
			IntervalCount:    p.IntervalCount,
			PriceStr:         fmt.Sprintf("$%.0f", p.PriceUSD),
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

func RegisterRoutes(r gin.IRouter) {
	pub := r.Group("/public")
	pub.OPTIONS("/plans", func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		c.AbortWithStatus(204)
	})
	pub.GET("/plans", listPlans)
}
