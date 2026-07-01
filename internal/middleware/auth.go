package middleware

import (
	"net/http"
	"strings"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"
	"whatify/backend/pkg/jwtutil"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	CtxUserID       = "user_id"
	CtxTenantID     = "tenant_id"
	CtxRole         = "role"
	CtxIsSuperAdmin = "is_super_admin"
)

func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Accept token from header OR query param (needed for SSE/EventSource)
		tokenStr := ""
		header := c.GetHeader("Authorization")
		if strings.HasPrefix(header, "Bearer ") {
			tokenStr = strings.TrimPrefix(header, "Bearer ")
		} else if q := c.Query("token"); q != "" {
			tokenStr = q
		}

		if tokenStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}

		claims, err := jwtutil.Parse(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxTenantID, claims.TenantID)
		c.Set(CtxRole, claims.Role)
		c.Set(CtxIsSuperAdmin, claims.IsSuperAdmin)

		// Super admins bypass tenant-level restrictions
		if !claims.IsSuperAdmin {
			if blocked, code, msg := checkTenantAccess(claims.TenantID); blocked {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": msg, "code": code})
				return
			}
		}

		c.Next()
	}
}

// checkTenantAccess returns (blocked, code, message) if the tenant is
// suspended or its trial has expired. Expired paid subscriptions are
// auto-downgraded to STARTER rather than blocked.
func checkTenantAccess(tenantID uuid.UUID) (bool, string, string) {
	var tenant models.Tenant
	if err := database.DB.
		Select("id, is_suspended, plan, plan_expires_at, trial_ends_at, paytabs_token").
		First(&tenant, "id = ?", tenantID).Error; err != nil {
		// Fail closed — deny access on infrastructure errors to prevent
		// bypassing suspension/trial checks when the DB is temporarily down.
		return true, "system_error", "temporarily unable to verify access, please try again"
	}
	if tenant.IsSuspended {
		return true, "account_suspended", "account suspended — contact support"
	}
	// Paid subscription expired → auto-downgrade to STARTER (don't hard-block).
	// We only downgrade when there's no stored card token (cancelled / not renewed).
	if tenant.PlanExpiresAt != nil && time.Now().After(*tenant.PlanExpiresAt) && tenant.PaytabsToken == "" {
		if tenant.Plan != models.PlanStarter {
			database.DB.Model(&models.Tenant{}).Where("id = ?", tenantID).Updates(map[string]interface{}{
				"plan":            models.PlanStarter,
				"plan_expires_at": nil,
				"trial_ends_at":   nil, // clear trial so banner never resurfaces after downgrade
			})
		}
		return false, "", "" // allow through on STARTER
	}
	// Trial expired and never paid (plan_expires_at nil = never had a subscription)
	if tenant.TrialEndsAt != nil &&
		time.Now().After(*tenant.TrialEndsAt) &&
		tenant.PaytabsToken == "" &&
		tenant.PlanExpiresAt == nil {
		return true, "trial_expired", "your 3-day free trial has ended — subscribe to continue"
	}
	return false, "", ""
}

// AuthBillingBypass authenticates the request but skips the tenant access check.
// Used for billing endpoints so blocked/expired users can still subscribe.
func AuthBillingBypass() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := ""
		header := c.GetHeader("Authorization")
		if strings.HasPrefix(header, "Bearer ") {
			tokenStr = strings.TrimPrefix(header, "Bearer ")
		} else if q := c.Query("token"); q != "" {
			tokenStr = q
		}
		if tokenStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims, err := jwtutil.Parse(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxTenantID, claims.TenantID)
		c.Set(CtxRole, claims.Role)
		c.Set(CtxIsSuperAdmin, claims.IsSuperAdmin)
		c.Next()
	}
}

func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get(CtxRole)
		if role != "ADMIN" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin access required"})
			return
		}
		c.Next()
	}
}

func RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		isSuperAdmin, _ := c.Get(CtxIsSuperAdmin)
		if isSuperAdmin != true {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "super admin access required"})
			return
		}
		c.Next()
	}
}
