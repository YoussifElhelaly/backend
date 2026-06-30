package public

import (
	"log"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"
)

type defaultPlan struct {
	Name        string
	Label       string
	PriceUSD    float64
	Period      string
	Desc        string
	Badge       string
	CTA         string
	SortOrder   int
	Sessions    int
	MessagesDay int
	Agents      int
}

var defaultPlans = []defaultPlan{
	{
		Name:        "STARTER",
		Label:       "Starter",
		PriceUSD:    19,
		Period:      "mo",
		Desc:        "Small teams & solo operators",
		Badge:       "",
		CTA:         "Start free",
		SortOrder:   1,
		Sessions:    1,
		MessagesDay: 500,
		Agents:      2,
	},
	{
		Name:        "GROWTH",
		Label:       "Growth",
		PriceUSD:    49,
		Period:      "mo",
		Desc:        "Growing businesses",
		Badge:       "Most Popular",
		CTA:         "Start free",
		SortOrder:   2,
		Sessions:    5,
		MessagesDay: 5000,
		Agents:      10,
	},
	{
		Name:        "SCALE",
		Label:       "Scale",
		PriceUSD:    99,
		Period:      "mo",
		Desc:        "Agencies & enterprises",
		Badge:       "",
		CTA:         "Contact sales",
		SortOrder:   3,
		Sessions:    20,
		MessagesDay: -1,
		Agents:      -1,
	},
}

// SeedDefaultPlans ensures the 3 default plans exist in the plan_defs table.
// It ONLY creates plans that are missing — it NEVER overwrites existing plans.
// All plan fields (price, period, limits, interval_count, etc.) are managed
// exclusively via the Admin Dashboard after initial seeding.
func SeedDefaultPlans() {
	for _, p := range defaultPlans {
		var existing models.PlanDef
		if err := database.DB.Where("name = ?", p.Name).First(&existing).Error; err == nil {
			// Plan already exists — do NOT touch it. Admin may have customised it.
			continue
		}

		// New plan — insert with all default fields.
		plan := models.PlanDef{
			Name:          p.Name,
			Label:         p.Label,
			PriceUSD:      p.PriceUSD,
			Period:        p.Period,
			IntervalCount: 1,
			Desc:          p.Desc,
			Badge:         p.Badge,
			CTA:           p.CTA,
			SortOrder:     p.SortOrder,
			Sessions:      p.Sessions,
			MessagesDay:   p.MessagesDay,
			Agents:        p.Agents,
			IsCustom:      false,
			IsActive:      true,
		}
		if err := database.DB.Create(&plan).Error; err != nil {
			log.Printf("public: failed to seed plan %s: %v", p.Name, err)
		}
	}
	log.Println("public: default plans checked")
}
