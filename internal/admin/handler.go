package admin

import (
	"errors"
	"net/http"
	"runtime"
	"strconv"
	"time"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"
	"whatify/backend/pkg/jwtutil"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// DisconnectSession is wired from main.go to avoid import cycles.
var DisconnectSession func(sessionID string)

// ─── Stats ───────────────────────────────────────────────────────────────────

type PlatformStats struct {
	TotalTenants      int64            `json:"total_tenants"`
	TotalUsers        int64            `json:"total_users"`
	TotalSessions     int64            `json:"total_sessions"`
	ConnectedSessions int64            `json:"connected_sessions"`
	TotalMessages     int64            `json:"total_messages"`
	MessagesToday     int64            `json:"messages_today"`
	NewTenantsMonth   int64            `json:"new_tenants_month"`
	PlanBreakdown     map[string]int64 `json:"plan_breakdown"`
	MRR               float64          `json:"mrr"`
}

func handleStats(c *gin.Context) {
	db := database.DB
	var stats PlatformStats

	db.Model(&models.Tenant{}).Count(&stats.TotalTenants)
	db.Model(&models.User{}).Where("is_super_admin = false").Count(&stats.TotalUsers)
	db.Model(&models.WhatsAppSession{}).Count(&stats.TotalSessions)
	db.Model(&models.WhatsAppSession{}).Where("status = 'CONNECTED'").Count(&stats.ConnectedSessions)
	db.Model(&models.Message{}).Count(&stats.TotalMessages)

	todayStart := time.Now().UTC().Truncate(24 * time.Hour)
	db.Model(&models.Message{}).Where("created_at >= ?", todayStart).Count(&stats.MessagesToday)

	monthStart := time.Now().UTC().AddDate(0, -1, 0)
	db.Model(&models.Tenant{}).Where("created_at >= ?", monthStart).Count(&stats.NewTenantsMonth)

	stats.PlanBreakdown = map[string]int64{}
	for _, plan := range []string{"STARTER", "GROWTH", "SCALE"} {
		var count int64
		db.Model(&models.Tenant{}).Where("plan = ?", plan).Count(&count)
		stats.PlanBreakdown[plan] = count
	}

	for plan, count := range stats.PlanBreakdown {
		limits := billing.GetLimits(models.Plan(plan))
		stats.MRR += float64(count) * limits.PriceUSD
	}

	c.JSON(http.StatusOK, stats)
}

// ─── Tenants ─────────────────────────────────────────────────────────────────

type TenantRow struct {
	ID                uuid.UUID  `json:"id"`
	Name              string     `json:"name"`
	Plan              string     `json:"plan"`
	IsSuspended       bool       `json:"is_suspended"`
	PlanExpiresAt     *time.Time `json:"plan_expires_at"`
	DailyMessageLimit int        `json:"daily_message_limit"`
	UserCount         int64      `json:"user_count"`
	SessionCount      int64      `json:"session_count"`
	MessageCount      int64      `json:"message_count"`
	MessagesToday     int64      `json:"messages_today"`
	CreatedAt         time.Time  `json:"created_at"`
}

func handleListTenants(c *gin.Context) {
	db := database.DB
	q := c.Query("q")
	plan := c.Query("plan")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Build filtered tenant query
	tenantQuery := db.Model(&models.Tenant{}).Where("name != '__system__'")
	if q != "" {
		tenantQuery = tenantQuery.Where("name ILIKE ?", "%"+q+"%")
	}
	if plan != "" {
		tenantQuery = tenantQuery.Where("plan = ?", plan)
	}

	var total int64
	tenantQuery.Count(&total)

	var tenants []models.Tenant
	if err := tenantQuery.Order("created_at desc").Limit(limit).Offset(offset).Find(&tenants).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ids := make([]uuid.UUID, 0, len(tenants))
	for _, t := range tenants {
		ids = append(ids, t.ID)
	}

	// Bulk fetch counts in single queries instead of N+1
	type countRow struct {
		TenantID uuid.UUID
		Count    int64
	}
	todayStart := time.Now().UTC().Truncate(24 * time.Hour)

	userCounts := map[uuid.UUID]int64{}
	var userRows []countRow
	if len(ids) > 0 {
		db.Model(&models.User{}).
			Select("tenant_id, count(*) as count").
			Where("tenant_id IN ? AND is_super_admin = false", ids).
			Group("tenant_id").Scan(&userRows)
		for _, r := range userRows {
			userCounts[r.TenantID] = r.Count
		}
	}

	sessionCounts := map[uuid.UUID]int64{}
	var sessionRows []countRow
	if len(ids) > 0 {
		db.Model(&models.WhatsAppSession{}).
			Select("tenant_id, count(*) as count").
			Where("tenant_id IN ?", ids).
			Group("tenant_id").Scan(&sessionRows)
		for _, r := range sessionRows {
			sessionCounts[r.TenantID] = r.Count
		}
	}

	messageCounts := map[uuid.UUID]int64{}
	var messageRows []countRow
	if len(ids) > 0 {
		db.Model(&models.Message{}).
			Select("tenant_id, count(*) as count").
			Where("tenant_id IN ?", ids).
			Group("tenant_id").Scan(&messageRows)
		for _, r := range messageRows {
			messageCounts[r.TenantID] = r.Count
		}
	}

	todayCounts := map[uuid.UUID]int64{}
	var todayRows []countRow
	if len(ids) > 0 {
		db.Model(&models.Message{}).
			Select("tenant_id, count(*) as count").
			Where("tenant_id IN ? AND created_at >= ?", ids, todayStart).
			Group("tenant_id").Scan(&todayRows)
		for _, r := range todayRows {
			todayCounts[r.TenantID] = r.Count
		}
	}

	rows := make([]TenantRow, 0, len(tenants))
	for _, t := range tenants {
		rows = append(rows, TenantRow{
			ID:                t.ID,
			Name:              t.Name,
			Plan:              string(t.Plan),
			IsSuspended:       t.IsSuspended,
			PlanExpiresAt:     t.PlanExpiresAt,
			DailyMessageLimit: t.DailyMessageLimit,
			UserCount:         userCounts[t.ID],
			SessionCount:      sessionCounts[t.ID],
			MessageCount:      messageCounts[t.ID],
			MessagesToday:     todayCounts[t.ID],
			CreatedAt:         t.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"tenants": rows,
		"total":   total,
		"page":    page,
		"limit":   limit,
	})
}

type TenantDetail struct {
	models.Tenant
	Users         []models.User         `json:"users"`
	Sessions      []models.WhatsAppSession `json:"sessions"`
	Subscriptions []models.Subscription `json:"subscriptions"`
	MessageCount  int64                 `json:"message_count"`
	ContactCount  int64                 `json:"contact_count"`
}

func handleGetTenant(c *gin.Context) {
	id := c.Param("id")
	db := database.DB

	var tenant models.Tenant
	if err := db.Where("id = ?", id).First(&tenant).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
		return
	}

	detail := TenantDetail{Tenant: tenant}
	db.Where("tenant_id = ? AND is_super_admin = false", tenant.ID).Find(&detail.Users)
	db.Where("tenant_id = ?", tenant.ID).Find(&detail.Sessions)
	db.Where("tenant_id = ?", tenant.ID).Order("created_at desc").Limit(20).Find(&detail.Subscriptions)
	db.Model(&models.Message{}).Where("tenant_id = ?", tenant.ID).Count(&detail.MessageCount)
	db.Model(&models.Contact{}).Where("tenant_id = ?", tenant.ID).Count(&detail.ContactCount)

	c.JSON(http.StatusOK, detail)
}

type UpdateTenantRequest struct {
	Name              string     `json:"name"`
	Plan              string     `json:"plan"`
	IsSuspended       *bool      `json:"is_suspended"`
	PlanExpiresAt     *time.Time `json:"plan_expires_at"`
	DailyMessageLimit *int       `json:"daily_message_limit"`
}

func handleUpdateTenant(c *gin.Context) {
	id := c.Param("id")
	var req UpdateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var tenant models.Tenant
	if err := database.DB.Where("id = ?", id).First(&tenant).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
		return
	}

	updates := map[string]interface{}{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Plan != "" {
		updates["plan"] = req.Plan
	}
	if req.IsSuspended != nil {
		updates["is_suspended"] = *req.IsSuspended
	}
	if req.PlanExpiresAt != nil {
		updates["plan_expires_at"] = req.PlanExpiresAt
	}
	if req.DailyMessageLimit != nil {
		v := *req.DailyMessageLimit
		if v < 0 {
			v = 0
		}
		updates["daily_message_limit"] = v
	}

	if err := database.DB.Model(&tenant).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	database.DB.First(&tenant, "id = ?", id)
	c.JSON(http.StatusOK, tenant)
}

type CreateTenantRequest struct {
	Name              string `json:"name"               binding:"required,min=2"`
	Plan              string `json:"plan"               binding:"required"`
	DailyMessageLimit *int   `json:"daily_message_limit"`
	AdminName         string `json:"admin_name"         binding:"required"`
	AdminEmail        string `json:"admin_email"        binding:"required,email"`
	AdminPassword     string `json:"admin_password"     binding:"required,min=8"`
}

func handleCreateTenant(c *gin.Context) {
	var req CreateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var existing models.User
	if err := database.DB.Where("email = ?", req.AdminEmail).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
		return
	}

	limits := billing.GetLimits(models.Plan(req.Plan))
	tenant := models.Tenant{Name: req.Name, Plan: models.Plan(req.Plan), DailyMessageLimit: limits.MessagesDay}
	if req.DailyMessageLimit != nil {
		tenant.DailyMessageLimit = *req.DailyMessageLimit
	}
	if err := database.DB.Create(&tenant).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	user := models.User{
		TenantID:     tenant.ID,
		Name:         req.AdminName,
		Email:        req.AdminEmail,
		PasswordHash: string(hash),
		Role:         models.RoleAdmin,
	}
	if err := database.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"tenant": tenant, "user": gin.H{
		"id": user.ID, "name": user.Name, "email": user.Email,
	}})
}

func handleDeleteTenant(c *gin.Context) {
	id := c.Param("id")
	db := database.DB

	var tenant models.Tenant
	if err := db.Where("id = ?", id).First(&tenant).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
		return
	}

	// Soft-delete all tenant data. With soft delete, FK constraints are never
	// violated (rows stay in DB), so order doesn't matter. We cascade explicitly
	// so child records don't appear as orphaned active data.
	db.Where("tenant_id = ?", id).Delete(&models.Flow{})
	db.Where("tenant_id = ?", id).Delete(&models.Campaign{})
	db.Where("campaign_id IN (SELECT id FROM campaigns WHERE tenant_id = ?)", id).Delete(&models.CampaignContact{})
	db.Where("funnel_id IN (SELECT id FROM funnels WHERE tenant_id = ?)", id).Delete(&models.FunnelContact{})
	db.Where("funnel_id IN (SELECT id FROM funnels WHERE tenant_id = ?)", id).Delete(&models.FunnelStep{})
	db.Where("tenant_id = ?", id).Delete(&models.Funnel{})
	db.Where("conversation_id IN (SELECT id FROM conversations WHERE tenant_id = ?)", id).Delete(&models.Message{})
	db.Where("tenant_id = ?", id).Delete(&models.Conversation{})
	db.Where("tenant_id = ?", id).Delete(&models.Contact{})
	db.Where("tenant_id = ?", id).Delete(&models.Tag{})
	db.Where("tenant_id = ?", id).Delete(&models.WhatsAppSession{})
	db.Where("tenant_id = ?", id).Delete(&models.APIKey{})
	db.Where("tenant_id = ?", id).Delete(&models.Webhook{})
	db.Where("tenant_id = ?", id).Delete(&models.Product{})
	db.Where("tenant_id = ?", id).Delete(&models.QuickReply{})

	// Hard-delete audit/financial/history records (no DeletedAt on these models)
	db.Where("tenant_id = ?", id).Delete(&models.ActivityLog{})
	db.Where("funnel_id IN (SELECT id FROM funnels WHERE tenant_id = ?)", id).Delete(&models.FunnelContactHistory{})
	db.Where("tenant_id = ?", id).Delete(&models.Subscription{})

	// Finally soft-delete users then the tenant itself
	db.Where("tenant_id = ? AND is_super_admin = false", id).Delete(&models.User{})
	db.Delete(&tenant)

	c.JSON(http.StatusOK, gin.H{"message": "tenant deleted"})
}

// ─── Super Admin Setup ────────────────────────────────────────────────────────

// EnsureSuperAdmin creates a super admin if one doesn't exist yet.
// Called on server startup when SUPER_ADMIN_EMAIL + SUPER_ADMIN_PASSWORD env vars are set.
func EnsureSuperAdmin(email, password string) {
	var existing models.User
	if err := database.DB.Where("is_super_admin = true").First(&existing).Error; err == nil {
		return // already exists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return
	}

	// Super admins don't belong to a tenant — use a dedicated system tenant
	var sysTenant models.Tenant
	if err := database.DB.Where("name = '__system__'").First(&sysTenant).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		sysTenant = models.Tenant{Name: "__system__", Plan: models.PlanScale}
		database.DB.Create(&sysTenant)
	}

	superAdmin := models.User{
		TenantID:     sysTenant.ID,
		Name:         "Super Admin",
		Email:        email,
		PasswordHash: string(hash),
		Role:         models.RoleAdmin,
		IsSuperAdmin: true,
	}
	database.DB.Create(&superAdmin)
}

// ─── Super Admin Token (re-login to get super admin JWT) ─────────────────────

type SuperAdminLoginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type SuperAdminLoginResponse struct {
	Token        string `json:"token"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	IsSuperAdmin bool   `json:"is_super_admin"`
}

func handleSuperAdminLogin(c *gin.Context) {
	var req SuperAdminLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	if err := database.DB.Where("email = ? AND is_super_admin = true", req.Email).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token, err := jwtutil.Generate(user.ID, user.TenantID, string(user.Role), true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, SuperAdminLoginResponse{
		Token:        token,
		Name:         user.Name,
		Email:        user.Email,
		IsSuperAdmin: true,
	})
}

// ─── Charts ───────────────────────────────────────────────────────────────────

type DayBucket struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

type MonthBucket struct {
	Month  string  `json:"month"`
	Amount float64 `json:"amount"`
}

type ChartsResponse struct {
	MRRTrend []MonthBucket `json:"mrr_trend"`
	Signups  []DayBucket   `json:"signups"`
	Messages []DayBucket   `json:"messages"`
}

func handleCharts(c *gin.Context) {
	db := database.DB
	now := time.Now().UTC()

	// Parse period param for signups and messages (7d, 30d, 90d)
	period := c.DefaultQuery("period", "30d")
	days := 30
	switch period {
	case "7d":
		days = 7
	case "90d":
		days = 90
	}

	// MRR trend — paid subscriptions per calendar month, last 12 months
	mrrTrend := make([]MonthBucket, 12)
	for i := 11; i >= 0; i-- {
		t := now.AddDate(0, -i, 0)
		label := t.Format("Jan 2006")
		monthStart := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		monthEnd := monthStart.AddDate(0, 1, 0)

		var total float64
		db.Model(&models.Subscription{}).
			Select("COALESCE(SUM(amount),0)").
			Where("status = 'PAID' AND paid_at >= ? AND paid_at < ?", monthStart, monthEnd).
			Scan(&total)

		mrrTrend[11-i] = MonthBucket{Month: label, Amount: total}
	}

	// Daily signups
	signups := make([]DayBucket, days)
	for i := days - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		dayEnd := dayStart.AddDate(0, 0, 1)
		label := dayStart.Format("Jan 2")

		var count int64
		db.Model(&models.Tenant{}).
			Where("name != '__system__' AND created_at >= ? AND created_at < ?", dayStart, dayEnd).
			Count(&count)
		signups[days-1-i] = DayBucket{Date: label, Count: count}
	}

	// Daily messages
	messages := make([]DayBucket, days)
	for i := days - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
		dayEnd := dayStart.AddDate(0, 0, 1)
		label := dayStart.Format("Jan 2")

		var count int64
		db.Model(&models.Message{}).
			Where("created_at >= ? AND created_at < ?", dayStart, dayEnd).
			Count(&count)
		messages[days-1-i] = DayBucket{Date: label, Count: count}
	}

	c.JSON(http.StatusOK, ChartsResponse{
		MRRTrend: mrrTrend,
		Signups:  signups,
		Messages: messages,
	})
}

// ─── Sessions Monitor ─────────────────────────────────────────────────────────

type AdminSessionRow struct {
	ID         uuid.UUID  `json:"id"`
	Phone      string     `json:"phone"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	TenantName string     `json:"tenant_name"`
	Status     string     `json:"status"`
	ProxyURL   string     `json:"proxy_url,omitempty"`
	DailyCount int        `json:"daily_count"`
	DailyLimit int        `json:"daily_limit"`
	CreatedAt  time.Time  `json:"created_at"`
	LastActive *time.Time `json:"last_active,omitempty"`
}

func handleListAdminSessions(c *gin.Context) {
	db := database.DB
	status := c.Query("status")
	q := c.Query("q")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	baseQuery := func(tx *gorm.DB) *gorm.DB {
		tx = tx.Joins("JOIN tenants ON tenants.id = whatsapp_sessions.tenant_id").
			Where("tenants.name != '__system__' AND tenants.deleted_at IS NULL")
		if status != "" {
			tx = tx.Where("whatsapp_sessions.status = ?", status)
		}
		if q != "" {
			tx = tx.Where("whatsapp_sessions.phone ILIKE ?", "%"+q+"%")
		}
		return tx
	}

	var total int64
	countTx := baseQuery(db.Model(&models.WhatsAppSession{}))
	countTx.Count(&total)

	var sessions []models.WhatsAppSession
	findTx := baseQuery(db.Preload("Tenant"))
	if err := findTx.Order("whatsapp_sessions.created_at desc").
		Limit(limit).Offset(offset).
		Find(&sessions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows := make([]AdminSessionRow, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, AdminSessionRow{
			ID:         s.ID,
			Phone:      s.Phone,
			TenantID:   s.TenantID,
			TenantName: s.Tenant.Name,
			Status:     string(s.Status),
			ProxyURL:   s.ProxyURL,
			DailyCount: s.DailyCount,
			DailyLimit: s.Tenant.DailyMessageLimit,
			CreatedAt:  s.CreatedAt,
			LastActive: s.LastActive,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"sessions": rows,
		"total":    total,
		"page":     page,
		"limit":    limit,
	})
}

func handleDisconnectSession(c *gin.Context) {
	id := c.Param("id")

	var session models.WhatsAppSession
	if err := database.DB.Where("id = ?", id).First(&session).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if session.Status != models.StatusConnected {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session is not connected"})
		return
	}

	if DisconnectSession != nil {
		DisconnectSession(id)
	}

	if err := database.DB.Model(&session).Update("status", models.StatusDisconnected).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "session disconnected"})
}

// ─── Global Activity Log ──────────────────────────────────────────────────────

type AdminActivityRow struct {
	models.ActivityLog
	TenantName string `json:"tenant_name"`
}

func handleAdminActivity(c *gin.Context) {
	db := database.DB

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	tenantFilter := c.Query("tenant_id")
	action := c.Query("action")
	entityType := c.Query("entity_type")

	var total int64
	q := db.Model(&models.ActivityLog{})
	if tenantFilter != "" {
		q = q.Where("tenant_id = ?", tenantFilter)
	}
	if action != "" {
		q = q.Where("action = ?", action)
	}
	if entityType != "" {
		q = q.Where("entity_type = ?", entityType)
	}
	q.Count(&total)

	var logs []models.ActivityLog
	q2 := db.Order("created_at desc").Limit(limit).Offset(offset)
	if tenantFilter != "" {
		q2 = q2.Where("tenant_id = ?", tenantFilter)
	}
	if action != "" {
		q2 = q2.Where("action = ?", action)
	}
	if entityType != "" {
		q2 = q2.Where("entity_type = ?", entityType)
	}
	q2.Find(&logs)

	// Enrich with tenant names and user names
	tenantCache := map[uuid.UUID]string{}
	userCache := map[uuid.UUID]string{}

	rows := make([]AdminActivityRow, 0, len(logs))
	for _, l := range logs {
		row := AdminActivityRow{ActivityLog: l}

		if name, ok := tenantCache[l.TenantID]; ok {
			row.TenantName = name
		} else {
			var t models.Tenant
			if err := db.Select("name").Where("id = ?", l.TenantID).First(&t).Error; err == nil {
				tenantCache[l.TenantID] = t.Name
				row.TenantName = t.Name
			}
		}

		if l.UserID != nil {
			if name, ok := userCache[*l.UserID]; ok {
				row.UserName = name
			} else {
				var u models.User
				if err := db.Select("name").Where("id = ?", l.UserID).First(&u).Error; err == nil {
					userCache[*l.UserID] = u.Name
					row.UserName = u.Name
				}
			}
		}

		rows = append(rows, row)
	}

	c.JSON(http.StatusOK, gin.H{
		"logs":  rows,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// ─── System Health ───────────────────────────────────────────────────────────

var serverStartTime = time.Now()

type SystemHealth struct {
	UptimeSeconds    int64  `json:"uptime_seconds"`
	DBStatus         string `json:"db_status"`
	ConnectedSessions int64 `json:"connected_sessions"`
	TotalSessions    int64  `json:"total_sessions"`
	TotalTenants     int64  `json:"total_tenants"`
	TotalUsers       int64  `json:"total_users"`
	GoVersion        string `json:"go_version"`
	MemoryMB         float64 `json:"memory_mb"`
	CPUThreads       int    `json:"cpu_threads"`
}

func handleSystemHealth(c *gin.Context) {
	db := database.DB

	// DB health check
	dbStatus := "OK"
	sqlDB, err := db.DB()
	if err != nil || sqlDB.Ping() != nil {
		dbStatus = "ERROR"
	}

	var h SystemHealth
	h.UptimeSeconds = int64(time.Since(serverStartTime).Seconds())
	h.DBStatus = dbStatus
	h.GoVersion = runtime.Version()
	h.CPUThreads = runtime.GOMAXPROCS(0)

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	h.MemoryMB = float64(memStats.Alloc) / 1024 / 1024

	db.Model(&models.WhatsAppSession{}).Where("status = ?", models.StatusConnected).Count(&h.ConnectedSessions)
	db.Model(&models.WhatsAppSession{}).Count(&h.TotalSessions)
	db.Model(&models.Tenant{}).Where("name != '__system__'").Count(&h.TotalTenants)
	db.Model(&models.User{}).Where("is_super_admin = false").Count(&h.TotalUsers)

	c.JSON(http.StatusOK, h)
}

// ─── Bulk Plan Change ────────────────────────────────────────────────────────

func handleBulkPlanChange(c *gin.Context) {
	var req struct {
		TenantIDs []string `json:"tenant_ids" binding:"required,min=1"`
		Plan      string   `json:"plan"      binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	plan := models.Plan(req.Plan)
	// Validate plan exists (built-in or custom)
	var planDef models.PlanDef
	if err := database.DB.Where("name = ? AND is_active = true", req.Plan).First(&planDef).Error; err != nil {
		// Check built-in plans
		validBuiltin := map[models.Plan]bool{models.PlanStarter: true, models.PlanGrowth: true, models.PlanScale: true}
		if !validBuiltin[plan] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plan"})
			return
		}
	}

	ids := make([]uuid.UUID, 0, len(req.TenantIDs))
	for _, idStr := range req.TenantIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant ID: " + idStr})
			return
		}
		ids = append(ids, id)
	}

	result := database.DB.Model(&models.Tenant{}).
		Where("id IN ? AND name != '__system__'", ids).
		Update("plan", plan)

	c.JSON(http.StatusOK, gin.H{
		"message":     "bulk plan updated",
		"updated_count": result.RowsAffected,
	})
}
