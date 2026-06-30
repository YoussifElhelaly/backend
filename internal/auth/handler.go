package auth

import (
	"log"
	"net/http"
	"time"
	"whatify/backend/internal/middleware"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func RegisterRoutes(r *gin.RouterGroup) {
	rl := middleware.RateLimit(10, time.Minute)
	auth := r.Group("/auth")
	{
		auth.POST("/register", rl, handleRegister)
		auth.POST("/login", rl, handleLogin)
		auth.GET("/verify-email", handleVerifyEmail)
		auth.POST("/resend-verification", rl, handleResendVerification)
		auth.POST("/forgot-password", rl, handleForgotPassword)
		auth.POST("/reset-password", handleResetPassword)
		auth.POST("/refresh", handleRefresh)
		auth.GET("/me", middleware.Auth(), handleMe)
		auth.PUT("/me", middleware.Auth(), handleUpdateProfile)
		auth.PUT("/password", middleware.Auth(), handleChangePassword)
	}
}

func handleRegister(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := register(req)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, resp)
}

func handleVerifyEmail(c *gin.Context) {
	var req VerifyEmailRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing token"})
		return
	}

	resp, err := verifyEmail(req.Token)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func handleResendVerification(c *gin.Context) {
	var req ResendVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resendVerification(req.Email)
	// Always 200 — don't reveal whether email is registered.
	c.JSON(http.StatusOK, gin.H{"message": "if that email is registered and unverified, a new link has been sent"})
}

func handleLogin(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := login(req)
	if err != nil {
		if err.Error() == "email_not_verified" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Please verify your email address before signing in.",
				"code":  "email_not_verified",
				"email": req.Email,
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func handleMe(c *gin.Context) {
	userID, _ := c.Get(middleware.CtxUserID)
	user, err := getMe(userID.(uuid.UUID).String())
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, user)
}

func handleUpdateProfile(c *gin.Context) {
	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get(middleware.CtxUserID)
	user, err := updateProfile(userID.(uuid.UUID).String(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, user)
}

func handleForgotPassword(c *gin.Context) {
	var req ForgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Always return 200 to avoid leaking whether an email exists
	if err := forgotPassword(req.Email); err != nil {
		log.Printf("[AUTH] forgot-password email error for %s: %v", req.Email, err)
	}
	c.JSON(http.StatusOK, gin.H{"message": "if that email is registered, a reset link has been sent"})
}

func handleResetPassword(c *gin.Context) {
	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := resetPassword(req.Token, req.NewPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "password updated successfully"})
}

func handleRefresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := refreshTokens(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func handleChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get(middleware.CtxUserID)
	if err := changePassword(userID.(uuid.UUID).String(), req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "password updated"})
}
