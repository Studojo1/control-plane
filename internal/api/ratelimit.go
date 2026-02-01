package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/studojo/control-plane/internal/auth"
)

// RateLimitConfig defines rate limiting configuration for different endpoint types
type RateLimitConfig struct {
	Requests int           // Number of requests allowed
	Window   time.Duration // Time window
}

// RateLimiter handles rate limiting using Redis
type RateLimiter struct {
	client *redis.Client
	config map[string]RateLimitConfig
}

// NewRateLimiter creates a new rate limiter with Redis client
func NewRateLimiter(redisURL string) (*RateLimiter, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		// If parsing fails, try to create client with default options
		opts = &redis.Options{
			Addr:     "localhost:6379",
			Password: "",
			DB:       0,
		}
		if redisURL != "" {
			// Try to extract host:port from URL
			opts.Addr = redisURL
		}
	}

	client := redis.NewClient(opts)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		slog.Warn("Redis connection failed, rate limiting disabled", "error", err)
		return nil, nil // Return nil to indicate rate limiting is disabled
	}

	// Default rate limit configurations
	config := map[string]RateLimitConfig{
		"auth":     {Requests: 5, Window: time.Minute},   // 5 requests per minute for auth
		"payment":  {Requests: 10, Window: time.Minute},   // 10 requests per minute for payments
		"admin":    {Requests: 30, Window: time.Minute},   // 30 requests per minute for admin
		"default":  {Requests: 100, Window: time.Minute},  // 100 requests per minute for general endpoints
	}

	return &RateLimiter{
		client: client,
		config: config,
	}, nil
}

// getEndpointType determines the endpoint type from the request path
func (rl *RateLimiter) getEndpointType(path string) string {
	if rl == nil {
		return "default"
	}

	// Check for auth-related endpoints
	if contains(path, []string{"/auth", "/login", "/signin", "/signup"}) {
		return "auth"
	}

	// Check for payment endpoints
	if contains(path, []string{"/payment", "/pay", "/order"}) {
		return "payment"
	}

	// Check for admin endpoints
	if contains(path, []string{"/admin"}) {
		return "admin"
	}

	return "default"
}

func contains(s string, substrs []string) bool {
	for _, substr := range substrs {
		if len(s) >= len(substr) && s[:len(substr)] == substr {
			return true
		}
	}
	return false
}

// RateLimit middleware that limits requests per user/IP
func (rl *RateLimiter) RateLimit(next http.Handler) http.Handler {
	if rl == nil || rl.client == nil {
		// Rate limiting disabled, pass through
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for health/ready endpoints
		if r.URL.Path == "/health" || r.URL.Path == "/ready" {
			next.ServeHTTP(w, r)
			return
		}

		// Get identifier (user ID from context if available, otherwise IP)
		identifier := r.RemoteAddr
		if userID := auth.UserIDFromContext(r.Context()); userID != "" {
			identifier = "user:" + userID
		} else {
			// Extract IP from RemoteAddr (format: "IP:port")
			if idx := len(identifier) - 1; idx >= 0 {
				for i := idx; i >= 0; i-- {
					if identifier[i] == ':' {
						identifier = identifier[:i]
						break
					}
				}
			}
			identifier = "ip:" + identifier
		}

		endpointType := rl.getEndpointType(r.URL.Path)
		cfg := rl.config[endpointType]

		// Create Redis key
		key := fmt.Sprintf("ratelimit:%s:%s", endpointType, identifier)

		ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
		defer cancel()

		// Use sliding window log algorithm
		now := time.Now().Unix()
		windowStart := now - int64(cfg.Window.Seconds())

		// Remove old entries
		rl.client.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(windowStart, 10))

		// Count current requests in window
		count, err := rl.client.ZCard(ctx, key).Result()
		if err != nil {
			slog.Warn("rate limit check failed", "error", err)
			// On error, allow request but log warning
			next.ServeHTTP(w, r)
			return
		}

		if count >= int64(cfg.Requests) {
			// Rate limit exceeded
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(cfg.Requests))
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("Retry-After", strconv.FormatInt(int64(cfg.Window.Seconds()), 10))
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		// Add current request to the set
		member := fmt.Sprintf("%d", now)
		rl.client.ZAdd(ctx, key, redis.Z{
			Score:  float64(now),
			Member: member,
		})
		rl.client.Expire(ctx, key, cfg.Window+time.Second)

		// Set rate limit headers
		remaining := cfg.Requests - int(count) - 1
		if remaining < 0 {
			remaining = 0
		}
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(cfg.Requests))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(now+int64(cfg.Window.Seconds()), 10))

		next.ServeHTTP(w, r)
	})
}

