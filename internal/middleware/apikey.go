package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"

	"github.com/gin-gonic/gin"
)

type apiRLBucket struct {
	count   int
	resetAt time.Time
	mu      sync.Mutex
}

// APIKeyAuth authenticates requests via X-API-Key header or ?api_key= query param.
// Sets CtxTenantID in context for downstream handlers.
// Also applies per-key rate limiting (120 req/min).
func APIKeyAuth() gin.HandlerFunc {
	var store sync.Map

	// Cleanup expired buckets every minute.
	go func() {
		for range time.NewTicker(time.Minute).C {
			store.Range(func(k, v any) bool {
				b := v.(*apiRLBucket)
				b.mu.Lock()
				expired := time.Now().After(b.resetAt)
				b.mu.Unlock()
				if expired {
					store.Delete(k)
				}
				return true
			})
		}
	}()

	return func(c *gin.Context) {
		key := c.GetHeader("X-API-Key")
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing api key — pass X-API-Key header",
				"code":  "missing_api_key",
			})
			return
		}

		apiKey, err := validateAPIKeyRaw(key)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid api key",
				"code":  "invalid_api_key",
			})
			return
		}

		// Per-key rate limiting: 120 requests per minute.
		keyID := apiKey.ID.String()
		val, _ := store.LoadOrStore(keyID, &apiRLBucket{resetAt: time.Now().Add(time.Minute)})
		b := val.(*apiRLBucket)

		b.mu.Lock()
		if time.Now().After(b.resetAt) {
			b.count = 0
			b.resetAt = time.Now().Add(time.Minute)
		}
		b.count++
		count := b.count
		b.mu.Unlock()

		if count > 120 {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded — 120 requests per minute per API key",
				"code":  "rate_limit_exceeded",
			})
			return
		}

		c.Set(CtxTenantID, apiKey.TenantID)

		remaining := 120 - count
		if remaining < 0 {
			remaining = 0
		}
		c.Header("X-RateLimit-Limit", "120")
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

		c.Next()
	}
}

func validateAPIKeyRaw(rawKey string) (*models.APIKey, error) {
	sum := sha256.Sum256([]byte(rawKey))
	hash := hex.EncodeToString(sum[:])

	var key models.APIKey
	if err := database.DB.Where("key_hash = ?", hash).First(&key).Error; err != nil {
		return nil, err
	}

	database.DB.Model(&key).Update("last_used_at", "NOW()")
	return &key, nil
}

// CtxAPIKeyID is the context key for the API key UUID (optional, for handlers that need it).
const CtxAPIKeyID = "api_key_id"
