// Package features defines the feature constants and provides helpers for
// plan-based feature gating. It is imported by both billing and middleware
// without creating circular dependencies.
package features

import (
	"encoding/json"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"
)

// Feature constants
const (
	Inbox        = "inbox"
	Contacts     = "contacts"
	Campaigns    = "campaigns"
	Funnels      = "funnels"
	Flows        = "flows"
	Analytics    = "analytics"
	Activity     = "activity"
	Products     = "products"
	QuickReplies = "quick_replies"
	AI           = "ai"
	APIKeys      = "api_keys"
	Webhooks     = "webhooks"
	Team         = "team"
)

// AllFeatures is the complete list of gateable features.
var AllFeatures = []string{
	Inbox, Contacts, Campaigns, Funnels,
	Flows, Analytics, Activity, Products,
	QuickReplies, AI, APIKeys, Webhooks, Team,
}

// FeatureLabels provides human-readable names for the admin UI.
var FeatureLabels = map[string]string{
	Inbox:        "Inbox & Messaging",
	Contacts:     "Contacts",
	Campaigns:    "Campaigns",
	Funnels:      "Sales Funnels",
	Flows:        "Automation Flows",
	Analytics:    "Analytics",
	Activity:     "Activity Log",
	Products:     "Product Catalog",
	QuickReplies: "Quick Replies",
	AI:           "AI Assistant",
	APIKeys:      "Developer API",
	Webhooks:     "Webhooks",
	Team:         "Team Management",
}

// DefaultFeatures per built-in plan.
var DefaultFeatures = map[models.Plan][]string{
	models.PlanStarter: {
		Inbox, Contacts,
	},
	models.PlanGrowth: {
		Inbox, Contacts, Campaigns, Flows,
		Analytics, Products, QuickReplies, Team,
	},
	models.PlanScale: {
		Inbox, Contacts, Campaigns, Funnels,
		Flows, Analytics, Activity, Products,
		QuickReplies, AI, APIKeys, Webhooks, Team,
	},
}

// ParseFeatures decodes a JSON features string into a string slice.
func ParseFeatures(raw string) []string {
	var features []string
	if raw == "" || raw == "null" {
		return features
	}
	if err := json.Unmarshal([]byte(raw), &features); err != nil {
		return features
	}
	return features
}

// ToJSON encodes a string slice into a JSON string for DB storage.
func ToJSON(features []string) string {
	if features == nil {
		features = []string{}
	}
	b, _ := json.Marshal(features)
	return string(b)
}

// GetPlanFeatures returns the feature list for a tenant's plan.
// Checks PlanDef table first, falls back to built-in defaults.
func GetPlanFeatures(plan models.Plan) []string {
	var planDef models.PlanDef
	if err := database.DB.Where("name = ? AND is_active = true", plan).First(&planDef).Error; err == nil {
		f := ParseFeatures(planDef.Features)
		if len(f) > 0 {
			return f
		}
	}
	if f, ok := DefaultFeatures[plan]; ok {
		return f
	}
	return DefaultFeatures[models.PlanStarter]
}

// HasFeature checks if a tenant has a specific feature enabled.
func HasFeature(tenant models.Tenant, feature string) bool {
	f := GetPlanFeatures(tenant.Plan)
	for _, feat := range f {
		if feat == feature {
			return true
		}
	}
	return false
}
