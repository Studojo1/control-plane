package dodo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Client wraps the Dodo Payments API.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewClient creates a Dodo Payments client.
func NewClient(apiKey string, testMode bool) *Client {
	baseURL := "https://live.dodopayments.com"
	if testMode {
		baseURL = "https://test.dodopayments.com"
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// CheckoutRequest for creating a Dodo checkout session.
type CheckoutRequest struct {
	ProductCart []ProductCartItem `json:"product_cart"`
	Customer    Customer          `json:"customer"`
	ReturnURL   string            `json:"return_url"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ProductCartItem represents a product in the cart.
type ProductCartItem struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

// Customer for the checkout.
type Customer struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// CheckoutResponse from Dodo API.
type CheckoutResponse struct {
	SessionID   string `json:"session_id"`
	CheckoutURL string `json:"checkout_url"`
}

// CreateCheckout creates a checkout session.
func (c *Client) CreateCheckout(req CheckoutRequest) (*CheckoutResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.baseURL+"/checkout_sessions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("dodo api call: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("dodo api error %d: %s", resp.StatusCode, string(respBody))
	}

	var result CheckoutResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	slog.Info("dodo checkout created", "session_id", result.SessionID)
	return &result, nil
}
