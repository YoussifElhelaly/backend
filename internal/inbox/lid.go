package inbox

import (
	"log"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
)

// ResolveContactLID is wired from main.go to session.Mgr.ResolveContactLID.
// Given a session and a phone string, returns the real phone number if the
// input was a LID, or "" if it was already a real phone / resolution failed.
var ResolveContactLID func(sessionID uuid.UUID, phone string) string

// FetchContactInfo is wired from main.go to session.Mgr.GetContactInfo.
var FetchContactInfo func(sessionID uuid.UUID, phone string) (string, string)

// ResolveLIDContacts scans all contacts for the given session and resolves any
// LID-format phone numbers to real phone numbers. If a real-phone contact
// already exists, it merges by moving conversations and deleting the LID contact.
// It also checks and fills in missing names from the local store.
// Called as a goroutine after HandleHistorySync completes.
func ResolveLIDContacts(sessionID, tenantID uuid.UUID) {
	if ResolveContactLID == nil {
		return
	}

	var contacts []models.Contact
	if err := database.DB.Where("session_id = ? AND tenant_id = ?", sessionID, tenantID).
		Find(&contacts).Error; err != nil {
		log.Printf("lid resolve: fetch contacts: %v", err)
		return
	}

	resolved := 0
	namesUpdated := 0
	for _, contact := range contacts {
		updated := false
		realPhone := ResolveContactLID(sessionID, contact.PhoneNumber)
		
		// If it's a LID, resolve it
		if realPhone != "" && realPhone != contact.PhoneNumber {
			log.Printf("lid resolve: %s -> %s (contact %s)", contact.PhoneNumber, realPhone, contact.ID)

			var existing models.Contact
			err := database.DB.
				Where("session_id = ? AND tenant_id = ? AND phone_number = ?", sessionID, tenantID, realPhone).
				First(&existing).Error

			if err == nil {
				// Real-phone contact already exists — move conversations then delete LID contact
				database.DB.Model(&models.Conversation{}).
					Where("contact_id = ?", contact.ID).
					Update("contact_id", existing.ID)
				database.DB.Delete(&contact)
				log.Printf("lid resolve: merged contact %s into %s", contact.ID, existing.ID)
				contact = existing // continue operating on the merged real contact
			} else {
				// No collision — just fix the phone number in place
				database.DB.Model(&contact).Update("phone_number", realPhone)
				contact.PhoneNumber = realPhone
				updated = true
			}
			resolved++
		}

		// Also try to fill missing names
		if FetchContactInfo != nil && (contact.Name == "" || contact.PushName == "") {
			fullName, pushName := FetchContactInfo(sessionID, contact.PhoneNumber)
			if fullName != "" && contact.Name != fullName {
				contact.Name = fullName
				database.DB.Model(&contact).Update("name", fullName)
				updated = true
			}
			if pushName != "" && contact.PushName != pushName {
				contact.PushName = pushName
				database.DB.Model(&contact).Update("push_name", pushName)
				updated = true
			}
			if updated {
				namesUpdated++
			}
		}

		// Also try to fill missing avatar
		if FetchAvatar != nil && contact.AvatarURL == "" {
			url, avatarID := FetchAvatar(sessionID, contact.PhoneNumber)
			if url != "" {
				database.DB.Model(&contact).Updates(map[string]interface{}{"avatar_url": url, "avatar_id": avatarID})
				contact.AvatarURL = url
				contact.AvatarID = avatarID
				updated = true
			}
		}
	}

	if resolved > 0 || namesUpdated > 0 {
		log.Printf("lid resolve: fixed %d LID contact(s) and updated %d name(s) for session %s", resolved, namesUpdated, sessionID)
	}
}
