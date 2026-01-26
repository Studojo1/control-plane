package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// JWKSClient fetches and caches JWKS for JWT verification.
type JWKSClient interface {
	VerifyToken(ctx context.Context, raw string) (claims *TokenClaims, err error)
}

// TokenClaims extracted from JWT (e.g. sub = user_id).
type TokenClaims struct {
	Sub string
}

// JWKSClientImpl fetches JWKS from URL, caches it, and verifies JWTs.
type JWKSClientImpl struct {
	url     string
	client  *http.Client
	mu      sync.RWMutex
	keySet  *jose.JSONWebKeySet
	fetched time.Time
	ttl     time.Duration
}

// NewJWKSClient returns a JWKS client that fetches from url and caches (indefinitely per plan; refetch on kid miss).
func NewJWKSClient(url string, httpClient *http.Client) *JWKSClientImpl {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &JWKSClientImpl{
		url:    url,
		client: httpClient,
		ttl:    24 * time.Hour,
	}
}

// VerifyToken parses and verifies the JWT, returns claims (sub = user_id).
func (c *JWKSClientImpl) VerifyToken(ctx context.Context, raw string) (*TokenClaims, error) {
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{
		jose.EdDSA, jose.RS256, jose.RS384, jose.RS512,
		jose.ES256, jose.ES384, jose.ES512,
		jose.PS256, jose.PS384, jose.PS512,
	})
	if err != nil {
		return nil, fmt.Errorf("parse jwt: %w", err)
	}

	keySet, err := c.getKeySet(ctx)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}

	kid := ""
	for _, h := range tok.Headers {
		if h.KeyID != "" {
			kid = h.KeyID
			break
		}
	}

	var key *jose.JSONWebKey
	if kid != "" {
		keys := keySet.Key(kid)
		if len(keys) > 0 {
			key = &keys[0]
		}
	}
	if key == nil {
		for i := range keySet.Keys {
			key = &keySet.Keys[i]
			break
		}
	}
	if key == nil {
		return nil, fmt.Errorf("no key found in JWKS")
	}

	var claims jwt.Claims
	if err := tok.Claims(key.Key, &claims); err != nil {
		return nil, fmt.Errorf("verify jwt: %w", err)
	}

	exp := jwt.Expected{Time: time.Now().UTC()}
	if err := claims.Validate(exp); err != nil {
		return nil, fmt.Errorf("validate claims: %w", err)
	}

	return &TokenClaims{Sub: claims.Subject}, nil
}

func (c *JWKSClientImpl) getKeySet(ctx context.Context) (*jose.JSONWebKeySet, error) {
	c.mu.RLock()
	if c.keySet != nil && time.Since(c.fetched) < c.ttl {
		ks := c.keySet
		c.mu.RUnlock()
		return ks, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keySet != nil && time.Since(c.fetched) < c.ttl {
		return c.keySet, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks status %d", resp.StatusCode)
	}

	var raw struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}

	var keys []jose.JSONWebKey
	for _, b := range raw.Keys {
		var jwk jose.JSONWebKey
		if err := jwk.UnmarshalJSON(b); err != nil {
			continue
		}
		keys = append(keys, jwk)
	}
	c.keySet = &jose.JSONWebKeySet{Keys: keys}
	c.fetched = time.Now().UTC()
	return c.keySet, nil
}

// Refresh clears the cache so the next VerifyToken refetches JWKS.
func (c *JWKSClientImpl) Refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keySet = nil
}
