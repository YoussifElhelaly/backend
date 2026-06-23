package admin

import (
	"errors"
	"net/http"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"
	"whatify/backend/pkg/jwtutil"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

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

	planPrices := map[string]float64{"STARTER": 19, "GROWTH": 49, "SCALE": 99}
	for plan, count := range stats.PlanBreakdown {
		stats.MRR += float64(count) * planPrices[plan]
	}

	c.JSON(http.StatusOK, stats)
}

// ─── Tenants ─────────────────────────────────────────────────────────────────

type TenantRow struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	Plan          string     `json:"plan"`
	IsSuspended   bool       `json:"is_suspended"`
	PlanExpiresAt *time.Time `json:"plan_expires_at"`
	UserCount     int64      `json:"user_count"`
	SessionCount  int64      `json:"session_count"`
	MessageCount  int64      `json:"message_count"`
	MessagesToday int64      `json:"messages_today"`
	CreatedAt     time.Time  `json:"created_at"`
}

func handleListTenants(c *gin.Context) {
	db := database.DB
	q := c.Query("q")
	plan := c.Query("plan")

	var tenants []models.Tenant
	tx := db.Order("created_at desc")
	if q != "" {
		tx = tx.Where("name ILIKE ?", "%"+q+"%")
	}
	if plan != "" {
		tx = tx.Where("plan = ?", plan)
	}
	if err := tx.Find(&tenants).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	todayStart := time.Now().UTC().Truncate(24 * time.Hour)
	rows := make([]TenantRow, 0, len(tenants))
	for _, t := range tenants {
		row := TenantRow{
			ID:            t.ID,
			Name:          t.Name,
			Plan:          string(t.Plan),
			IsSuspended:   t.IsSuspended,
			PlanExpiresAt: t.PlanExpiresAt,
			CreatedAt:     t.CreatedAt,
		}
		db.Model(&models.User{}).Where("tenant_id = ? AND is_super_admin = false", t.ID).Count(&row.UserCount)
		db.Model(&models.WhatsAppSession{}).Where("tenant_id = ?", t.ID).Count(&row.SessionCount)
		db.Model(&models.Message{}).Where("tenant_id = ?", t.ID).Count(&row.MessageCount)
		db.Model(&models.Message{}).Where("tenant_id = ? AND created_at >= ?", t.ID, todayStart).Count(&row.MessagesToday)
		rows = append(rows, row)
	}

	c.JSON(http.StatusOK, rows)
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
	Name          string     `json:"name"`
	Plan          string     `json:"plan"`
	IsSuspended   *bool      `json:"is_suspended"`
	PlanExpiresAt *time.Time `json:"plan_expires_at"`
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

	if err := database.DB.Model(&tenant).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	database.DB.First(&tenant, "id = ?", id)
	c.JSON(http.StatusOK, tenant)
}

type CreateTenantRequest struct {
	Name          string `json:"name"           binding:"required,min=2"`
	Plan          string `json:"plan"           binding:"required"`
	AdminName     string `json:"admin_name"     binding:"required"`
	AdminEmail    string `json:"admin_email"    binding:"required,email"`
	AdminPassword string `json:"admin_password" binding:"required,min=8"`
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

	tenant := models.Tenant{Name: req.Name, Plan: models.Plan(req.Plan)}
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

// ─── Subscriptions ────────────────────────────────────────────────────────────

type SubscriptionRow struct {
	models.Subscription
	TenantName string `json:"tenant_name"`
}

func handleListSubscriptions(c *gin.Context) {
	var subs []models.Subscription
	if err := database.DB.Order("created_at desc").Limit(100).Find(&subs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows := make([]SubscriptionRow, 0, len(subs))
	for _, s := range subs {
		row := SubscriptionRow{Subscription: s}
		var tenant models.Tenant
		if err := database.DB.Where("id = ?", s.TenantID).First(&tenant).Error; err == nil {
			row.TenantName = tenant.Name
		}
		rows = append(rows, row)
	}

	c.JSON(http.StatusOK, rows)
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
