package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"
	"whatify/backend/pkg/jwtutil"
	"whatify/backend/pkg/mailer"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func register(req RegisterRequest) (*RegisterResponse, error) {
	var existing models.User
	if err := database.DB.Where("email = ?", req.Email).First(&existing).Error; err == nil {
		return nil, errors.New("email already registered")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	var user models.User
	trialEnd := time.Now().Add(3 * 24 * time.Hour)
	tenant := models.Tenant{Name: req.CompanyName, Plan: models.PlanStarter, TrialEndsAt: &trialEnd}

	// Wrap tenant + user creation in a transaction to prevent orphaned tenants.
	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&tenant).Error; err != nil {
			return err
		}
		user = models.User{
			TenantID:        tenant.ID,
			Name:            req.Name,
			Email:           req.Email,
			PasswordHash:    string(hash),
			Role:            models.RoleAdmin,
			IsEmailVerified: false,
		}
		return tx.Create(&user).Error
	}); err != nil {
		return nil, err
	}

	// Send verification email in background — don't fail registration if SMTP is down.
	go sendVerificationEmail(user)

	return &RegisterResponse{
		Message: "Account created. Please check your email to verify your address.",
		Email:   user.Email,
	}, nil
}

func sendVerificationEmail(user models.User) {
	database.DB.Where("user_id = ?", user.ID).Delete(&models.EmailVerificationToken{})

	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)

	if err := database.DB.Create(&models.EmailVerificationToken{
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}).Error; err != nil {
		return
	}

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}
	verifyURL := frontendURL + "/verify-email?token=" + token

	body := fmt.Sprintf(`<div style="font-family:sans-serif;max-width:480px;margin:0 auto">
<h2 style="color:#111">Verify your Whatify email</h2>
<p>Hi %s, click the button below to verify your email address. The link expires in 24 hours.</p>
<p><a href="%s" style="display:inline-block;background:#25D366;color:#fff;padding:12px 24px;border-radius:8px;text-decoration:none;font-weight:600">Verify email address</a></p>
<p style="color:#888;font-size:13px">If you didn't create a Whatify account, you can ignore this email.</p>
</div>`, user.Name, verifyURL)

	mailer.Send(user.Email, "Verify your Whatify email", body)
}

func verifyEmail(token string) (*AuthResponse, error) {
	var vt models.EmailVerificationToken
	if err := database.DB.Where("token = ?", token).First(&vt).Error; err != nil {
		return nil, errors.New("invalid or expired verification link")
	}
	if time.Now().After(vt.ExpiresAt) {
		database.DB.Delete(&vt)
		return nil, errors.New("verification link has expired — please request a new one")
	}

	if err := database.DB.Model(&models.User{}).
		Where("id = ?", vt.UserID).
		Update("is_email_verified", true).Error; err != nil {
		return nil, err
	}
	database.DB.Delete(&vt)

	var user models.User
	if err := database.DB.Preload("Tenant").First(&user, "id = ?", vt.UserID).Error; err != nil {
		return nil, errors.New("user not found")
	}

	jwtToken, err := jwtutil.Generate(user.ID, user.TenantID, string(user.Role), user.IsSuperAdmin)
	if err != nil {
		return nil, err
	}
	refreshToken, err := issueRefreshToken(user.ID)
	if err != nil {
		return nil, err
	}

	resp := buildAuthResponse(jwtToken, user, user.Tenant)
	resp.RefreshToken = refreshToken
	return resp, nil
}

func resendVerification(email string) {
	var user models.User
	if err := database.DB.Where("email = ?", email).First(&user).Error; err != nil {
		return // don't reveal whether email exists
	}
	if user.IsEmailVerified {
		return
	}
	go sendVerificationEmail(user)
}

// MigrateExistingVerifications marks users created before email verification was
// introduced as verified so they aren't locked out after the deploy.
func MigrateExistingVerifications() {
	result := database.DB.Model(&models.User{}).
		Where("is_email_verified = ? AND is_super_admin = ?", false, false).
		Update("is_email_verified", true)
	if result.RowsAffected > 0 {
		fmt.Printf("auth: marked %d existing users as email-verified\n", result.RowsAffected)
	}
}

func forgotPassword(email string) error {
	var user models.User
	if err := database.DB.Where("email = ?", email).First(&user).Error; err != nil {
		return nil // don't reveal whether email exists
	}

	database.DB.Where("user_id = ?", user.ID).Delete(&models.PasswordResetToken{})

	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)

	if err := database.DB.Create(&models.PasswordResetToken{
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}).Error; err != nil {
		return err
	}

	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}
	resetURL := frontendURL + "/reset-password?token=" + token

	body := fmt.Sprintf(`<h2>Reset your Whatify password</h2>
<p>Click the link below. It expires in 1 hour.</p>
<p><a href="%s" style="background:#25D366;color:#fff;padding:10px 20px;border-radius:6px;text-decoration:none;display:inline-block">Reset Password</a></p>
<p>If you didn't request this, ignore this email.</p>`, resetURL)

	return mailer.Send(user.Email, "Reset your Whatify password", body)
}

func resetPassword(token, newPassword string) error {
	var rt models.PasswordResetToken
	if err := database.DB.Where("token = ?", token).First(&rt).Error; err != nil {
		return errors.New("invalid or expired reset link")
	}
	if time.Now().After(rt.ExpiresAt) {
		database.DB.Delete(&rt)
		return errors.New("reset link has expired")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := database.DB.Model(&models.User{}).
		Where("id = ?", rt.UserID).
		Update("password_hash", string(hash)).Error; err != nil {
		return err
	}
	database.DB.Delete(&rt)
	return nil
}

func issueRefreshToken(userID uuid.UUID) (string, error) {
	database.DB.Where("user_id = ? AND expires_at < ?", userID, time.Now()).
		Delete(&models.RefreshToken{})

	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)

	if err := database.DB.Create(&models.RefreshToken{
		UserID:    userID,
		Token:     token,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}).Error; err != nil {
		return "", err
	}
	return token, nil
}

func refreshTokens(refreshToken string) (*AuthResponse, error) {
	var rt models.RefreshToken
	if err := database.DB.Where("token = ?", refreshToken).First(&rt).Error; err != nil {
		return nil, errors.New("invalid refresh token")
	}
	if time.Now().After(rt.ExpiresAt) {
		database.DB.Delete(&rt)
		return nil, errors.New("refresh token expired — please log in again")
	}

	// Token rotation: delete old, issue new
	database.DB.Delete(&rt)

	var user models.User
	if err := database.DB.Preload("Tenant").First(&user, "id = ?", rt.UserID).Error; err != nil {
		return nil, errors.New("user not found")
	}

	accessToken, err := jwtutil.Generate(user.ID, user.TenantID, string(user.Role), user.IsSuperAdmin)
	if err != nil {
		return nil, err
	}

	newRefreshToken, err := issueRefreshToken(user.ID)
	if err != nil {
		return nil, err
	}

	resp := buildAuthResponse(accessToken, user, user.Tenant)
	resp.RefreshToken = newRefreshToken
	return resp, nil
}

func login(req LoginRequest) (*AuthResponse, error) {
	var user models.User
	if err := database.DB.Preload("Tenant").Where("email = ?", req.Email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("invalid credentials")
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, errors.New("invalid credentials")
	}

	if !user.IsEmailVerified && !user.IsSuperAdmin {
		return nil, errors.New("email_not_verified")
	}

	token, err := jwtutil.Generate(user.ID, user.TenantID, string(user.Role), user.IsSuperAdmin)
	if err != nil {
		return nil, err
	}

	refreshToken, err := issueRefreshToken(user.ID)
	if err != nil {
		return nil, err
	}

	resp := buildAuthResponse(token, user, user.Tenant)
	resp.RefreshToken = refreshToken
	return resp, nil
}

func getMe(userID string) (*UserDTO, error) {
	var user models.User
	if err := database.DB.Where("id = ?", userID).First(&user).Error; err != nil {
		return nil, errors.New("user not found")
	}
	dto := &UserDTO{
		ID:    user.ID.String(),
		Name:  user.Name,
		Email: user.Email,
		Role:  string(user.Role),
	}
	return dto, nil
}

func updateProfile(userID string, req UpdateProfileRequest) (*UserDTO, error) {
	var user models.User
	if err := database.DB.Where("id = ?", userID).First(&user).Error; err != nil {
		return nil, errors.New("user not found")
	}

	user.Name = req.Name
	if err := database.DB.Save(&user).Error; err != nil {
		return nil, err
	}

	return &UserDTO{
		ID:    user.ID.String(),
		Name:  user.Name,
		Email: user.Email,
		Role:  string(user.Role),
	}, nil
}

func changePassword(userID string, req ChangePasswordRequest) error {
	var user models.User
	if err := database.DB.Where("id = ?", userID).First(&user).Error; err != nil {
		return errors.New("user not found")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		return errors.New("current password is incorrect")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	return database.DB.Model(&user).Update("password_hash", string(hash)).Error
}

func buildAuthResponse(token string, user models.User, tenant models.Tenant) *AuthResponse {
	return &AuthResponse{
		Token: token,
		User: UserDTO{
			ID:           user.ID.String(),
			Name:         user.Name,
			Email:        user.Email,
			Role:         string(user.Role),
			IsSuperAdmin: user.IsSuperAdmin,
		},
		Tenant: TenantDTO{
			ID:          tenant.ID.String(),
			Name:        tenant.Name,
			Plan:        string(tenant.Plan),
			TrialEndsAt: tenant.TrialEndsAt,
		},
	}
}
