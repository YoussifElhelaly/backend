package settings

import (
	"net/http"
	"whatify/backend/internal/billing"
	"whatify/backend/internal/middleware"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type AddMemberInput struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role" binding:"required,oneof=ADMIN AGENT"`
}

type UpdateRoleInput struct {
	Role string `json:"role" binding:"required,oneof=ADMIN AGENT"`
}

func listTeam(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	callerID := c.MustGet(middleware.CtxUserID).(uuid.UUID)

	var members []models.User
	database.DB.Where("tenant_id = ? AND id != ?", tenantID, callerID).
		Select("id, tenant_id, name, email, role, created_at, updated_at").
		Order("created_at ASC").
		Find(&members)

	c.JSON(http.StatusOK, members)
}

func addMember(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	callerRole := c.MustGet(middleware.CtxRole).(string)

	if callerRole != string(models.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only admins can add team members"})
		return
	}

	if err := billing.CheckAgentLimit(tenantID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	var input AddMemberInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var existing models.User
	if err := database.DB.Where("email = ?", input.Email).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Email already registered"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create member"})
		return
	}

	member := models.User{
		TenantID:     tenantID,
		Name:         input.Name,
		Email:        input.Email,
		PasswordHash: string(hash),
		Role:         models.Role(input.Role),
	}

	if err := database.DB.Create(&member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create member"})
		return
	}

	member.PasswordHash = ""
	c.JSON(http.StatusCreated, member)
}

func updateMemberRole(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	callerRole := c.MustGet(middleware.CtxRole).(string)
	memberID := c.Param("id")

	if callerRole != string(models.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only admins can change roles"})
		return
	}

	var input UpdateRoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var member models.User
	if err := database.DB.Where("id = ? AND tenant_id = ?", memberID, tenantID).First(&member).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Member not found"})
		return
	}

	member.Role = models.Role(input.Role)
	database.DB.Save(&member)
	member.PasswordHash = ""
	c.JSON(http.StatusOK, member)
}

func removeMember(c *gin.Context) {
	tenantID := c.MustGet(middleware.CtxTenantID).(uuid.UUID)
	callerRole := c.MustGet(middleware.CtxRole).(string)
	memberID := c.Param("id")

	if callerRole != string(models.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only admins can remove members"})
		return
	}

	var member models.User
	if err := database.DB.Where("id = ? AND tenant_id = ?", memberID, tenantID).First(&member).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Member not found"})
		return
	}

	database.DB.Delete(&member)
	c.JSON(http.StatusOK, gin.H{"message": "Member removed"})
}
