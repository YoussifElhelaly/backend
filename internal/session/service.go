package session

import (
	"errors"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/google/uuid"
)

func list(tenantID uuid.UUID) ([]SessionResponse, error) {
	var sessions []models.WhatsAppSession
	if err := database.DB.Where("tenant_id = ?", tenantID).
		Order("created_at desc").
		Find(&sessions).Error; err != nil {
		return nil, err
	}

	out := make([]SessionResponse, len(sessions))
	for i, s := range sessions {
		out[i] = toDTO(s)
	}
	return out, nil
}

func create(tenantID uuid.UUID, req CreateSessionRequest) (*models.WhatsAppSession, error) {
	s := models.WhatsAppSession{
		TenantID: tenantID,
		Status:   models.StatusDisconnected,
		ProxyURL: req.ProxyURL,
	}
	if err := database.DB.Create(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func deleteSession(tenantID uuid.UUID, sessionID string) error {
	id, err := uuid.Parse(sessionID)
	if err != nil {
		return errors.New("invalid session id")
	}

	var s models.WhatsAppSession
	if err := database.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&s).Error; err != nil {
		return errors.New("session not found")
	}

	Mgr.Disconnect(sessionID)

	return database.DB.Delete(&s).Error
}

func toDTO(s models.WhatsAppSession) SessionResponse {
	r := SessionResponse{
		ID:         s.ID.String(),
		Phone:      s.Phone,
		Status:     string(s.Status),
		DailyCount: s.DailyCount,
		ProxyURL:   s.ProxyURL,
		CreatedAt:  s.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if s.LastActive != nil {
		r.LastActive = s.LastActive.Format("2006-01-02T15:04:05Z")
	}
	return r
}
