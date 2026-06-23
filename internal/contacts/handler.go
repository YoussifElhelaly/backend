package contacts

import (
	"fmt"
	"net/http"
	"whatify/backend/internal/activity"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/internal/session"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func listContacts(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	sessionPhone := c.Query("session_phone")
	search := c.Query("q")

	limit := 1000

	var contacts []models.Contact
	query := database.DB.Preload("Tags").Where("tenant_id = ?", tenantID)

	if sessionPhone != "" {
		var sess models.WhatsAppSession
		if err := database.DB.Where("tenant_id = ? AND phone = ?", tenantID, sessionPhone).First(&sess).Error; err == nil {
			query = query.Where("session_id = ?", sess.ID)
		} else {
			c.JSON(http.StatusOK, []models.Contact{})
			return
		}
	}

	if search != "" {
		query = query.Where("name ILIKE ? OR phone_number ILIKE ?", "%"+search+"%", "%"+search+"%")
	}

	if err := query.Order("name ASC").Limit(limit).Find(&contacts).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch contacts"})
		return
	}
	c.JSON(http.StatusOK, contacts)
}

type UpdateContactInput struct {
	Name string       `json:"name"`
	Tags *[]uuid.UUID `json:"tags"` // array of tag IDs
}

func updateContact(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	contactID := c.Param("id")

	var input UpdateContactInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var contact models.Contact
	if err := database.DB.Preload("Tags").Where("id = ? AND tenant_id = ?", contactID, tenantID).First(&contact).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Contact not found"})
		return
	}

	meta := map[string]string{"phone": contact.PhoneNumber}

	if input.Name != "" && input.Name != contact.Name {
		meta["name_before"] = contact.Name
		meta["name_after"] = input.Name
		contact.Name = input.Name
		database.DB.Save(&contact)
	}

	// Update tags
	if input.Tags != nil {
		// collect before names
		beforeNames := ""
		for i, t := range contact.Tags {
			if i > 0 {
				beforeNames += ", "
			}
			beforeNames += t.Name
		}

		var newTags []models.Tag
		if len(*input.Tags) > 0 {
			database.DB.Where("id IN ? AND tenant_id = ?", *input.Tags, tenantID).Find(&newTags)
		}
		database.DB.Model(&contact).Association("Tags").Replace(newTags)

		afterNames := ""
		for i, t := range newTags {
			if i > 0 {
				afterNames += ", "
			}
			afterNames += t.Name
		}
		if beforeNames != afterNames {
			meta["tags_before"] = beforeNames
			meta["tags_after"] = afterNames
		}
	}

	database.DB.Preload("Tags").First(&contact, "id = ?", contact.ID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	activity.Log(tenantID, &userID, "contact.updated", "contact", contact.ID.String(), meta)
	c.JSON(http.StatusOK, contact)
}

func syncContacts(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	sessionPhone := c.Param("id")

	var sess models.WhatsAppSession
	if err := database.DB.Where("tenant_id = ? AND phone = ?", tenantID, sessionPhone).First(&sess).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session not found"})
		return
	}

	client := session.Mgr.GetClient(sess.ID.String())
	if client == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "WhatsApp client is not connected"})
		return
	}

	contacts, err := client.Store.Contacts.GetAllContacts(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch contacts from device"})
		return
	}

	for jid, info := range contacts {
		if info.PushName == "" && info.FullName == "" {
			continue
		}
		name := info.FullName
		if name == "" {
			name = info.PushName
		}

		var contact models.Contact
		err := database.DB.Where("tenant_id = ? AND session_id = ? AND phone_number = ?", tenantID, sess.ID, jid.User).First(&contact).Error
		if err != nil {
			contact = models.Contact{
				TenantID:    tenantID,
				SessionID:   sess.ID,
				PhoneNumber: jid.User,
				Name:        name,
				PushName:    info.PushName,
			}
			database.DB.Create(&contact)
		} else {
			contact.PushName = info.PushName
			database.DB.Save(&contact)
		}
	}

	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)
	activity.Log(tenantID, &userID, "contacts.synced", "session", sess.ID.String(), map[string]string{
		"count": fmt.Sprintf("%d", len(contacts)),
		"phone": sess.Phone,
	})
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Synced %d contacts", len(contacts))})
}

func deleteContacts(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	userID := c.MustGet(middleware.CtxUserID).(uuid.UUID)

	type DeleteInput struct {
		IDs []uuid.UUID `json:"ids"`
	}
	var input DeleteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	if len(input.IDs) > 0 {
		for _, id := range input.IDs {
			var contact models.Contact
			if database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&contact).Error == nil {
				database.DB.Model(&contact).Association("Tags").Clear()
				database.DB.Delete(&contact)
			}
		}
		activity.Log(tenantID, &userID, "contacts.deleted", "contact", "", map[string]string{
			"count": fmt.Sprintf("%d", len(input.IDs)),
		})
	}

	c.JSON(http.StatusOK, gin.H{"message": "Contacts deleted"})
}
