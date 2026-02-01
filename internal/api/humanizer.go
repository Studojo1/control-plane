package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/studojo/control-plane/internal/auth"
	"github.com/studojo/control-plane/internal/pricing"
)

// HumanizerPriceRequest JSON body for POST /v1/humanizer/calculate-price.
type HumanizerPriceRequest struct {
	Payload json.RawMessage `json:"payload"` // Humanizer job payload with file_data
}

// HumanizerPriceResponse JSON response for POST /v1/humanizer/calculate-price.
type HumanizerPriceResponse struct {
	WordCount int `json:"word_count"`
	Amount    int `json:"amount"`    // Amount in paise
	AmountINR float64 `json:"amount_inr"` // Amount in INR (for display)
}

// HandleCalculateHumanizerPrice handles POST /v1/humanizer/calculate-price.
// Calculates the price for a humanizer job based on word count estimation.
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

	if len(req.Payload) == 0 {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "payload required")
		return
	}

	// Estimate word count from payload
	wordCount, err := pricing.EstimateWordCountFromPayload(req.Payload)
	if err != nil {
		slog.Error("failed to estimate word count", "error", err)
		WriteError(w, http.StatusBadRequest, ErrValidationFailed, "failed to estimate word count from payload")
		return
	}

	// Calculate price
	amount := pricing.CalculateHumanizerPrice(wordCount)
	amountINR := float64(amount) / 100.0

	WriteJSON(w, http.StatusOK, HumanizerPriceResponse{
		WordCount: wordCount,
		Amount:    amount,
		AmountINR: amountINR,
	})
}

