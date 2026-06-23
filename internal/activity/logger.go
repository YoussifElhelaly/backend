package activity

import (
	"fmt"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
)

// Log writes an activity entry asynchronously (fire-and-forget).
func Log(tenantID uuid.UUID, userID *uuid.UUID, action, entityType, entityID string, meta map[string]string) {
	go func() {
		metaStr := "{}"
		if len(meta) > 0 {
			metaStr = "{"
			first := true
			for k, v := range meta {
				if !first {
					metaStr += ","
				}
				metaStr += fmt.Sprintf("%q:%q", k, v)
				first = false
			}
			metaStr += "}"
		}

		entry := models.ActivityLog{
			TenantID:   tenantID,
			UserID:     userID,
			Action:     action,
			EntityType: entityType,
			EntityID:   entityID,
			Metadata:   metaStr,
		}
		database.DB.Create(&entry)
	}()
}
