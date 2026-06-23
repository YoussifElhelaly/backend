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
// suspended, its trial has expired, or its subscription has expired.
func checkTenantAccess(tenantID uuid.UUID) (bool, string, string) {
	var tenant models.Tenant
	if err := database.DB.
		Select("is_suspended, plan_expires_at, trial_ends_at, paypal_sub_id").
		First(&tenant, "id = ?", tenantID).Error; err != nil {
		return false, "", "" // on DB error, allow through (fail open)
	}
	if tenant.IsSuspended {
		return true, "account_suspended", "account suspended — contact support"
	}
	// Trial expired and no active subscription
	if tenant.TrialEndsAt != nil && time.Now().After(*tenant.TrialEndsAt) && tenant.PaypalSubID == "" {
		return true, "trial_expired", "your 3-day free trial has ended — subscribe to continue"
	}
	// Paid subscription expired (only checked when a subscription was previously active)
	if tenant.PlanExpiresAt != nil && time.Now().After(*tenant.PlanExpiresAt) {
		return true, "subscription_expired", "subscription expired — please renew your plan"
	}
	return false, "", ""
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
