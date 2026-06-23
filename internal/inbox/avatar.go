package inbox

import (
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
)

// FetchAvatar is wired by main.go to delegate profile picture fetching to the session manager.
var FetchAvatar func(sessionID uuid.UUID, phone string) (url, pictureID string)

func tryFetchAvatar(sessionID uuid.UUID, contact *models.Contact) {
	if FetchAvatar == nil {
		return
	}
	go func() {
		url, pid := FetchAvatar(sessionID, contact.PhoneNumber)
		if url == "" {
			return
		}
		database.DB.Model(contact).Updates(map[string]interface{}{
			"avatar_url": url,
			"avatar_id":  pid,
		})
	}()
}
