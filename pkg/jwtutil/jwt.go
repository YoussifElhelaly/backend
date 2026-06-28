package jwtutil

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	UserID       uuid.UUID `json:"user_id"`
	TenantID     uuid.UUID `json:"tenant_id"`
	Role         string    `json:"role"`
	IsSuperAdmin bool      `json:"is_super_admin"`
	jwt.RegisteredClaims
}

func Generate(userID, tenantID uuid.UUID, role string, isSuperAdmin bool) (string, error) {
	claims := Claims{
		UserID:       userID,
		TenantID:     tenantID,
		Role:         role,
		IsSuperAdmin: isSuperAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret()))
}

func Parse(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret()), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// ValidateSecret panics at startup if JWT_SECRET is not configured.
// Call this once from main() before serving any requests.
func ValidateSecret() {
	if os.Getenv("JWT_SECRET") == "" {
		panic("JWT_SECRET environment variable is not set — refusing to start")
	}
}

func secret() string {
	return os.Getenv("JWT_SECRET")
}
