package api

import (
	"encoding/json"
	"net/http"
)

// ErrorCode stable error codes for frontend consumption.
type ErrorCode string

const (
	ErrUnauthorized          ErrorCode = "unauthorized"
	ErrForbidden             ErrorCode = "forbidden"
	ErrInvalidIdempotencyKey ErrorCode = "invalid_idempotency_key"
	ErrValidationFailed      ErrorCode = "validation_failed"
	ErrJobNotFound           ErrorCode = "job_not_found"
	ErrPaymentRequired       ErrorCode = "payment_required"
	ErrPaymentFailed         ErrorCode = "payment_failed"
	ErrInternal              ErrorCode = "internal_error"
)

// ErrorResponse JSON body for errors.
type ErrorResponse struct {
	Error struct {
		Code    ErrorCode `json:"code"`
		Message string   `json:"message"`
	} `json:"error"`
}

// WriteJSON sets Content-Type and writes JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes ErrorResponse with given code and message.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	WriteJSON(w, status, ErrorResponse{
		Error: struct {
			Code    ErrorCode `json:"code"`
			Message string   `json:"message"`
		}{Code: code, Message: message},
	})
}
