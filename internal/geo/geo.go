package geo

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	cache  sync.Map
	client = &http.Client{Timeout: 3 * time.Second}
)

// DetectCountry returns the ISO country code from the request's X-Forwarded-For or RemoteAddr.
func DetectCountry(r *http.Request) string {
	ip := GetClientIP(r)
	if isPrivate(ip) {
		return "UNKNOWN"
	}
	if cached, ok := cache.Load(ip); ok {
		return cached.(string)
	}
	country := lookupCountry(ip)
	cache.Store(ip, country)
	return country
}

// IsIndia returns true if the request originates from India.
func IsIndia(r *http.Request) bool {
	return DetectCountry(r) == "IN"
}

// GetClientIP extracts client IP from X-Forwarded-For or RemoteAddr.
func GetClientIP(r *http.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isPrivate(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return true
	}
	return parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsUnspecified()
}

func lookupCountry(ip string) string {
	resp, err := client.Get(fmt.Sprintf("http://ip-api.com/json/%s?fields=countryCode,status", ip))
	if err != nil {
		slog.Warn("geo lookup failed", "ip", ip, "error", err)
		return "UNKNOWN"
	}
	defer resp.Body.Close()

	var result struct {
		Status      string `json:"status"`
		CountryCode string `json:"countryCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Warn("geo decode failed", "ip", ip, "error", err)
		return "UNKNOWN"
	}
	if result.Status != "success" {
		return "UNKNOWN"
	}
	return result.CountryCode
}
