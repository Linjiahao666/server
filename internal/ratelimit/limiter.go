package ratelimit

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/Linjiahao666/server/internal/httpx"
)

const (
	keyPrefix = "auth:rl:"
	window    = 60 * time.Second
)

const incrExpireScript = `
local n = redis.call("INCR", KEYS[1])
if n == 1 then
  redis.call("EXPIRE", KEYS[1], ARGV[1])
end
return n
`

// Limiter counts requests per client IP in Redis.
type Limiter struct {
	client *redis.Client
}

// NewLimiter creates a Redis-backed rate limiter.
func NewLimiter(client *redis.Client) *Limiter {
	return &Limiter{client: client}
}

// Allow rejects requests that exceed the per-IP limit for an action.
func (l *Limiter) Allow(action string, limit int) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := fmt.Sprintf("%s%s:%s", keyPrefix, action, c.ClientIP())
		count, err := l.client.Eval(c.Request.Context(), incrExpireScript, []string{key}, int(window.Seconds())).Int64()
		if err != nil {
			httpx.WriteError(c, httpx.StatusInternalError, "INTERNAL_ERROR", "failed to apply rate limit")
			return
		}
		if count > int64(limit) {
			httpx.WriteError(c, httpx.StatusTooManyRequests, "RATE_LIMITED", "too many requests")
			return
		}
		c.Next()
	}
}
