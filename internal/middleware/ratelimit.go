package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type rlBucket struct {
	count   int
	resetAt time.Time
	mu      sync.Mutex
}

// RateLimit limits requests to maxReqs per window per client IP.
// Each call returns an independent limiter with its own store.
func RateLimit(maxReqs int, window time.Duration) gin.HandlerFunc {
	var store sync.Map

	// Cleanup goroutine evicts expired buckets every window.
	go func() {
		for range time.NewTicker(window).C {
			store.Range(func(k, v any) bool {
				b := v.(*rlBucket)
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
		// Use X-Real-IP when behind a reverse proxy to prevent IP spoofing.
		ip := c.GetHeader("X-Real-IP")
		if ip == "" {
			ip = c.GetHeader("X-Forwarded-For")
			if idx := strings.Index(ip, ","); idx != -1 {
				ip = strings.TrimSpace(ip[:idx])
			}
		}
		if ip == "" {
			ip = c.ClientIP()
		}
		val, _ := store.LoadOrStore(ip, &rlBucket{resetAt: time.Now().Add(window)})
		b := val.(*rlBucket)

		b.mu.Lock()
		if time.Now().After(b.resetAt) {
			b.count = 0
			b.resetAt = time.Now().Add(window)
		}
		b.count++
		count := b.count
		b.mu.Unlock()

		if count > maxReqs {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests — please try again later",
				"code":  "rate_limit_exceeded",
			})
			return
		}
		c.Next()
	}
}
