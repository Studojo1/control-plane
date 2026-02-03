package api

import (
	"encoding/json"
	"net/http"

	"github.com/studojo/control-plane/internal/auth"
	"github.com/studojo/control-plane/internal/pricing"
)

// HumanizerPriceRequest JSON body for POST /v1/humanizer/calculate-price.
type HumanizerPriceRequest struct {
	WordCount int `json:"word_count"` // Actual word count from parsed DOCX
}

// HumanizerPriceResponse JSON response for POST /v1/humanizer/calculate-price.
type HumanizerPriceResponse struct {
	WordCount int `json:"word_count"`
	Amount    int `json:"amount"`    // Amount in paise
	AmountINR float64 `json:"amount_inr"` // Amount in INR (for display)
}

// HandleCalculateHumanizerPrice handles POST /v1/humanizer/calculate-price.
// Accepts word_count in JSON body (parsed client-side), calculates price.
func (h *Handler) HandleCalculateHumanizerPrice(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}

	var req HumanizerPriceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "invalid JSON body")
		return
	}

	if req.WordCount <= 0 {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "word_count must be greater than 0")
		return
	}

	// Calculate price
	amount := pricing.CalculateHumanizerPrice(req.WordCount)
	amountINR := float64(amount) / 100.0

	WriteJSON(w, http.StatusOK, HumanizerPriceResponse{
		WordCount: req.WordCount,
		Amount:    amount,
		AmountINR: amountINR,
	})
}

