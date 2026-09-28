package api

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimitConfig defines rate limiting configuration for different endpoint types
type RateLimitConfig struct {
	Requests int           // Number of requests allowed
	Window   time.Duration // Time window
}

// RateLimiter handles rate limiting using Redis.
//
// Keys are per client IP. The limiter wraps the router, before any route's
// auth middleware runs, so no user id is available here; per-IP is the key.
//
// Modes (RATE_LIMIT_MODE):
//   - "shadow" (default): count every request and log the ones that WOULD be
//     rejected, but let them through. Used to measure real traffic (a college
//     network can put many students behind one IP) before blocking anyone.
//   - "enforce": reject with 429 once a key is over its limit.
type RateLimiter struct {
	client  *redis.Client
	config  map[string]RateLimitConfig
	enforce bool
	prefix  string
}

// NewRateLimiter creates a new rate limiter with Redis client. It returns nil
// (rate limiting disabled) when Redis is unreachable at startup.
func NewRateLimiter(redisURL string) (*RateLimiter, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		opts = &redis.Options{Addr: "localhost:6379"}
		if redisURL != "" {
			opts.Addr = redisURL
		}
	}
	// The cluster's redis-url secret carries no password; Redis runs with
	// --requirepass, so take it from REDIS_PASSWORD when the URL has none.
	if opts.Password == "" {
		opts.Password = os.Getenv("REDIS_PASSWORD")
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		slog.Warn("Redis connection failed, rate limiting disabled", "error", err)
		return nil, nil
	}

	rl := newRateLimiter(client, os.Getenv("RATE_LIMIT_MODE"), os.Getenv("RATE_LIMIT_PREFIX"))
	slog.Info("rate limiting enabled", "enforce", rl.enforce, "prefix", rl.prefix)
	return rl, nil
}

func newRateLimiter(client *redis.Client, mode, prefix string) *RateLimiter {
	if prefix == "" {
		prefix = "ratelimit"
	}
	return &RateLimiter{
		client: client,
		config: map[string]RateLimitConfig{
			"auth":    {Requests: 5, Window: time.Minute},
			"payment": {Requests: 10, Window: time.Minute},
			"admin":   {Requests: 30, Window: time.Minute},
			"default": {Requests: 100, Window: time.Minute},
		},
		enforce: strings.EqualFold(strings.TrimSpace(mode), "enforce"),
		prefix:  prefix,
	}
}

// getEndpointType determines the endpoint type from the request path.
// Every control-plane route lives under /v1/, so the tiers match that; the
// old bare prefixes (/auth, /payment, /admin) never matched anything.
func (rl *RateLimiter) getEndpointType(path string) string {
	switch {
	case hasAnyPrefix(path, "/v1/auth", "/auth", "/login", "/signin", "/signup"):
		return "auth"
	case hasAnyPrefix(path, "/v1/payments", "/v1/payment", "/payment", "/pay"):
		return "payment"
	case hasAnyPrefix(path, "/v1/admin", "/admin"):
		return "admin"
	}
	return "default"
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// clientKey returns the rate-limit identity for a request, or exempt=true for
// in-cluster service-to-service calls.
//
// Traffic from the internet arrives through ingress-nginx, which sets
// X-Real-IP to the real client address (the load balancer runs with
// externalTrafficPolicy: Local, and use-forwarded-headers is off, so a client
// cannot spoof it). Keying on RemoteAddr instead put every visitor in one
// bucket: the ingress pod's IP.
//
// A request with no X-Real-IP from a private address did not come through the
// ingress: it is the frontend's or admin panel's server calling us directly,
// and many users share that one pod IP, so it is not limited here.
func clientKey(r *http.Request) (key string, exempt bool) {
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" && net.ParseIP(ip) != nil {
		return "ip:" + ip, false
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
		return "", true
	}
	return "ip:" + host, false
}

// RateLimit middleware that limits requests per client IP.
func (rl *RateLimiter) RateLimit(next http.Handler) http.Handler {
	if rl == nil || rl.client == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/ready" {
			next.ServeHTTP(w, r)
			return
		}
		identifier, exempt := clientKey(r)
		if exempt {
			next.ServeHTTP(w, r)
			return
		}

		endpointType := rl.getEndpointType(r.URL.Path)
		cfg := rl.config[endpointType]
		key := fmt.Sprintf("%s:%s:%s", rl.prefix, endpointType, identifier)

		ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
		defer cancel()

		// Sliding window log. Members must be unique per request: the old
		// member was the unix second, so every request inside one second
		// collapsed into a single entry and bursts were never counted.
		now := time.Now()
		windowStart := now.Add(-cfg.Window).UnixNano()
		pipe := rl.client.TxPipeline()
		pipe.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(windowStart, 10))
		card := pipe.ZCard(ctx, key)
		if _, err := pipe.Exec(ctx); err != nil {
			// Fail open: a Redis problem must never take the API down.
			slog.Warn("rate limit check failed", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		count := card.Val()

		over := count >= int64(cfg.Requests)
		if over {
			if rl.enforce {
				w.Header().Set("X-RateLimit-Limit", strconv.Itoa(cfg.Requests))
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("Retry-After", strconv.FormatInt(int64(cfg.Window.Seconds()), 10))
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			slog.Warn("rate limit would block (shadow mode)",
				"key", identifier, "type", endpointType, "count", count, "limit", cfg.Requests, "path", r.URL.Path)
		}

		member := fmt.Sprintf("%d-%d", now.UnixNano(), rand.Int63())
		pipe = rl.client.TxPipeline()
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(now.UnixNano()), Member: member})
		pipe.Expire(ctx, key, cfg.Window+time.Second)
		if _, err := pipe.Exec(ctx); err != nil {
			slog.Warn("rate limit record failed", "error", err)
		}

		remaining := cfg.Requests - int(count) - 1
		if remaining < 0 {
			remaining = 0
		}
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(cfg.Requests))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(now.Add(cfg.Window).Unix(), 10))

		next.ServeHTTP(w, r)
	})
}
