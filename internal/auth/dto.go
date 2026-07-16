package auth

import "time"

type RegisterRequest struct {
	CompanyName string `json:"company_name" binding:"required,min=2"`
	Name        string `json:"name"         binding:"required,min=2"`
	Email       string `json:"email"        binding:"required,email"`
	Password    string `json:"password"     binding:"required,min=8"`
}

type LoginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type UpdateProfileRequest struct {
	Name string `json:"name" binding:"required,min=2"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password"     binding:"required,min=8"`
}

type AuthResponse struct {
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	User         UserDTO   `json:"user"`
	Tenant       TenantDTO `json:"tenant"`
}

type ForgotPasswordRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type ResetPasswordRequest struct {
	Token       string `json:"token"        binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type RegisterResponse struct {
	Message string `json:"message"`
	Email   string `json:"email"`
}

type VerifyEmailRequest struct {
	Token string `form:"token" binding:"required"`
}

type ResendVerificationRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type UserDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	IsSuperAdmin bool   `json:"is_super_admin"`
}

type TenantDTO struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	Plan                string     `json:"plan"`
	TrialEndsAt         *time.Time `json:"trial_ends_at,omitempty"`
	OnboardingCompleted bool       `json:"onboarding_completed"`
}
