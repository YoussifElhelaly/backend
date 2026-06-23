package analytics

import (
	"net/http"
	"time"
	"whatify/backend/internal/middleware"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type DayBucket struct {
	Date     string `json:"date"`
	Incoming int    `json:"incoming"`
	Outgoing int    `json:"outgoing"`
}

type OverviewResponse struct {
	TotalConversations int         `json:"total_conversations"`
	OpenConversations  int         `json:"open_conversations"`
	ResolvedCount      int         `json:"resolved_count"`
	ResolutionRate     float64     `json:"resolution_rate"`
	TotalMessages      int         `json:"total_messages"`
	IncomingMessages   int         `json:"incoming_messages"`
	OutgoingMessages   int         `json:"outgoing_messages"`
	NewContacts        int         `json:"new_contacts"`
	MessagesPerDay     []DayBucket `json:"messages_per_day"`
	PeriodDays         int         `json:"period_days"`
}

func getOverview(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	sessionPhone := c.Query("session_phone")

	periodDays := 7
	switch c.Query("period") {
	case "30d":
		periodDays = 30
	case "90d":
		periodDays = 90
	}

	from := time.Now().UTC().AddDate(0, 0, -periodDays).Truncate(24 * time.Hour)
	to := time.Now().UTC()

	// Build session_id filter if session_phone given
	sessionFilter := ""
	var sessionArgs []interface{}
	if sessionPhone != "" {
		sessionFilter = " AND c.session_phone = ?"
		sessionArgs = append(sessionArgs, sessionPhone)
	}

	// Conversation counts
	var totalConvs, openConvs, resolvedConvs int
	database.DB.Raw(
		"SELECT COUNT(*) FROM conversations c WHERE c.tenant_id = ?"+sessionFilter,
		append([]interface{}{tenantID}, sessionArgs...)...,
	).Scan(&totalConvs)
	database.DB.Raw(
		"SELECT COUNT(*) FROM conversations c WHERE c.tenant_id = ? AND c.status = 'OPEN'"+sessionFilter,
		append([]interface{}{tenantID}, sessionArgs...)...,
	).Scan(&openConvs)
	database.DB.Raw(
		"SELECT COUNT(*) FROM conversations c WHERE c.tenant_id = ? AND c.status = 'RESOLVED'"+sessionFilter,
		append([]interface{}{tenantID}, sessionArgs...)...,
	).Scan(&resolvedConvs)

	resolutionRate := 0.0
	if totalConvs > 0 {
		resolutionRate = float64(resolvedConvs) / float64(totalConvs) * 100
	}

	// Message counts in period (join to conversations for session_phone filter)
	convJoin := "JOIN conversations c ON m.conversation_id = c.id"
	baseWhere := "m.tenant_id = ? AND m.is_note = false AND m.timestamp >= ? AND m.timestamp <= ?" + sessionFilter
	msgArgs := append([]interface{}{tenantID, from, to}, sessionArgs...)

	var totalMsgs, incomingMsgs, outgoingMsgs int
	database.DB.Raw("SELECT COUNT(*) FROM messages m "+convJoin+" WHERE "+baseWhere, msgArgs...).Scan(&totalMsgs)
	database.DB.Raw("SELECT COUNT(*) FROM messages m "+convJoin+" WHERE "+baseWhere+" AND m.direction = 'INCOMING'", msgArgs...).Scan(&incomingMsgs)
	database.DB.Raw("SELECT COUNT(*) FROM messages m "+convJoin+" WHERE "+baseWhere+" AND m.direction = 'OUTGOING'", msgArgs...).Scan(&outgoingMsgs)

	// New contacts in period
	var newContacts int
	newContactArgs := []interface{}{tenantID, from, to}
	newContactFilter := ""
	if sessionPhone != "" {
		newContactFilter = " AND session_id = (SELECT id FROM whats_app_sessions WHERE tenant_id = ? AND phone = ? LIMIT 1)"
		newContactArgs = append(newContactArgs, tenantID, sessionPhone)
	}
	database.DB.Raw(
		"SELECT COUNT(*) FROM contacts WHERE tenant_id = ? AND created_at >= ? AND created_at <= ?"+newContactFilter,
		newContactArgs...,
	).Scan(&newContacts)

	// Messages per day
	type rawBucket struct {
		Day      time.Time
		Incoming int
		Outgoing int
	}
	var rawBuckets []rawBucket
	database.DB.Raw(`
		SELECT
			DATE_TRUNC('day', m.timestamp) AS day,
			COUNT(*) FILTER (WHERE m.direction = 'INCOMING') AS incoming,
			COUNT(*) FILTER (WHERE m.direction = 'OUTGOING') AS outgoing
		FROM messages m
		`+convJoin+`
		WHERE `+baseWhere+`
		GROUP BY DATE_TRUNC('day', m.timestamp)
		ORDER BY day ASC
	`, msgArgs...).Scan(&rawBuckets)

	// Fill all days in range (even zeros)
	bucketMap := map[string]DayBucket{}
	for _, b := range rawBuckets {
		key := b.Day.Format("2006-01-02")
		bucketMap[key] = DayBucket{Date: key, Incoming: b.Incoming, Outgoing: b.Outgoing}
	}
	days := make([]DayBucket, 0, periodDays)
	for i := 0; i < periodDays; i++ {
		d := from.AddDate(0, 0, i).Format("2006-01-02")
		if b, ok := bucketMap[d]; ok {
			days = append(days, b)
		} else {
			days = append(days, DayBucket{Date: d, Incoming: 0, Outgoing: 0})
		}
	}

	c.JSON(http.StatusOK, OverviewResponse{
		TotalConversations: totalConvs,
		OpenConversations:  openConvs,
		ResolvedCount:      resolvedConvs,
		ResolutionRate:     resolutionRate,
		TotalMessages:      totalMsgs,
		IncomingMessages:   incomingMsgs,
		OutgoingMessages:   outgoingMsgs,
		NewContacts:        newContacts,
		MessagesPerDay:     days,
		PeriodDays:         periodDays,
	})
}
