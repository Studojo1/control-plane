package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestLimiter(t *testing.T, mode string) (*RateLimiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	return newRateLimiter(redis.NewClient(&redis.Options{Addr: mr.Addr()}), mode, "rl-test"), mr
}

func do(h http.Handler, path, remote, realIP string) int {
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = remote
	if realIP != "" {
		req.Header.Set("X-Real-IP", realIP)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

func TestClientKey(t *testing.T) {
	cases := []struct {
		remote, realIP, want string
		exempt               bool
	}{
		{"10.244.0.5:4321", "203.0.113.7", "ip:203.0.113.7", false}, // via ingress
		{"10.244.0.5:4321", "", "", true},                          // in-cluster service call
		{"127.0.0.1:1", "", "", true},
		{"198.51.100.9:80", "", "ip:198.51.100.9", false},       // public, no header
		{"10.244.0.5:4321", "not-an-ip", "", true},              // junk header ignored
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/v1/x", nil)
		req.RemoteAddr = c.remote
		if c.realIP != "" {
			req.Header.Set("X-Real-IP", c.realIP)
		}
		got, exempt := clientKey(req)
		if got != c.want || exempt != c.exempt {
			t.Errorf("clientKey(%s, %q) = %q,%v want %q,%v", c.remote, c.realIP, got, exempt, c.want, c.exempt)
		}
	}
}

func TestEndpointTypesMatchRealRoutes(t *testing.T) {
	rl := newRateLimiter(nil, "", "")
	for path, want := range map[string]string{
		"/v1/payments/create-order": "payment",
		"/v1/payments/verify-dodo":  "payment",
		"/v1/admin/users":           "admin",
		"/v1/outlines/generate":     "default",
		"/v1/email/events":          "default",
	} {
		if got := rl.getEndpointType(path); got != want {
			t.Errorf("%s: got %s want %s", path, got, want)
		}
	}
}

func TestShadowModeNeverBlocks(t *testing.T) {
	rl, _ := newTestLimiter(t, "")
	h := rl.RateLimit(ok)
	for i := 0; i < 250; i++ {
		if c := do(h, "/v1/x", "10.244.0.5:1", "203.0.113.7"); c != 200 {
			t.Fatalf("request %d blocked in shadow mode: %d", i, c)
		}
	}
}

func TestEnforceBlocksAtTheLimitAndCountsBursts(t *testing.T) {
	rl, _ := newTestLimiter(t, "enforce")
	h := rl.RateLimit(ok)
	// All inside one second: the old per-second member counted these as one.
	for i := 0; i < 100; i++ {
		if c := do(h, "/v1/x", "10.244.0.5:1", "203.0.113.7"); c != 200 {
			t.Fatalf("request %d rejected early: %d", i, c)
		}
	}
	if c := do(h, "/v1/x", "10.244.0.5:1", "203.0.113.7"); c != http.StatusTooManyRequests {
		t.Fatalf("101st request: got %d want 429", c)
	}
	// A different visitor has their own bucket.
	if c := do(h, "/v1/x", "10.244.0.5:1", "203.0.113.8"); c != 200 {
		t.Fatalf("second visitor blocked: %d", c)
	}
	// In-cluster calls are never limited.
	for i := 0; i < 300; i++ {
		if c := do(h, "/v1/x", "10.244.0.9:1", ""); c != 200 {
			t.Fatalf("internal call %d limited: %d", i, c)
		}
	}
}

func TestPaymentTierIsStricter(t *testing.T) {
	rl, _ := newTestLimiter(t, "enforce")
	h := rl.RateLimit(ok)
	for i := 0; i < 10; i++ {
		do(h, "/v1/payments/create-order", "10.244.0.5:1", "203.0.113.7")
	}
	if c := do(h, "/v1/payments/create-order", "10.244.0.5:1", "203.0.113.7"); c != http.StatusTooManyRequests {
		t.Fatalf("11th payment request: got %d want 429", c)
	}
}

func TestRedisFailureFailsOpen(t *testing.T) {
	rl, mr := newTestLimiter(t, "enforce")
	h := rl.RateLimit(ok)
	mr.Close()
	if c := do(h, "/v1/x", "10.244.0.5:1", "203.0.113.7"); c != 200 {
		t.Fatalf("redis down: got %d want 200 (fail open)", c)
	}
}
