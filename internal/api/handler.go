package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	razorpay "github.com/razorpay/razorpay-go"
	"github.com/studojo/control-plane/internal/auth"
	"github.com/studojo/control-plane/internal/pricing"
	"github.com/studojo/control-plane/internal/store"
	"github.com/studojo/control-plane/internal/workflow"
)

// ReadyChecker returns nil if DB and RabbitMQ are reachable.
type ReadyChecker interface {
	Ready(ctx context.Context) error
}

// Handler holds HTTP handlers for jobs, health, readiness, payments.
type Handler struct {
	Workflow     *workflow.Service
	Ready        ReadyChecker
	PaymentStore store.PaymentStore
	RazorpayKey  string
	RazorpaySecret string
}

// SubmitRequest JSON body for POST /v1/jobs.
type SubmitRequest struct {
	Type            string          `json:"type"`
	Payload         json.RawMessage `json:"payload"`
	PaymentOrderID  string          `json:"payment_order_id"` // Razorpay order ID - payment must be verified
	Outline         json.RawMessage `json:"outline,omitempty"` // Pre-generated outline for final generation
}

// OutlineGenerateRequest JSON body for POST /v1/outlines/generate.
type OutlineGenerateRequest struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// OutlineGenerateResponse JSON response for POST /v1/outlines/generate.
type OutlineGenerateResponse struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	Outline any    `json:"outline,omitempty"`
}

// OutlineEditRequest JSON body for POST /v1/outlines/edit.
type OutlineEditRequest struct {
	Outline     json.RawMessage `json:"outline"`
	UserMessage string          `json:"user_message"`
}

// OutlineEditResponse JSON response for POST /v1/outlines/edit.
type OutlineEditResponse struct {
	JobID            string `json:"job_id"`
	Status           string `json:"status"`
	Outline          any    `json:"outline,omitempty"`
	AssistantMessage string `json:"assistant_message,omitempty"`
}

// SubmitResponse JSON response for POST /v1/jobs (202 new, 200 replay).
type SubmitResponse struct {
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	Result    any    `json:"result,omitempty"`
}

// JobResponseJSON JSON response for GET /v1/jobs/:id.
type JobResponseJSON struct {
	JobID     string `json:"job_id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Result    any    `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

// PaymentVerifyRequest JSON body for POST /v1/payments/verify.
type PaymentVerifyRequest struct {
	RazorpayOrderID   string `json:"razorpay_order_id"`
	RazorpayPaymentID string `json:"razorpay_payment_id"`
	RazorpaySignature string `json:"razorpay_signature"`
	JobID             string `json:"job_id,omitempty"` // Optional: link payment to existing job
}

// PaymentVerifyResponse JSON response for POST /v1/payments/verify.
type PaymentVerifyResponse struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
	JobID     string `json:"job_id,omitempty"`
}

// PaymentCreateRequest JSON body for POST /v1/payments/create-order.
type PaymentCreateRequest struct {
	Amount  int             `json:"amount"`   // Amount in paise (e.g., 13900 for ₹139)
	JobType string          `json:"job_type,omitempty"` // Optional: "assignment-gen" or "humanizer" for price calculation
	Payload json.RawMessage `json:"payload,omitempty"`  // Optional: for humanizer word count estimation
}

// PaymentCreateResponse JSON response for POST /v1/payments/create-order.
type PaymentCreateResponse struct {
	OrderID string `json:"order_id"`
	Amount  int    `json:"amount"`
	KeyID   string `json:"key_id"` // Razorpay key ID for frontend
}

// HandleHealth returns 200 OK (liveness, no deps).
func (h *Handler) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// HandleReady returns 200 if ReadyChecker passes (DB + RabbitMQ).
func (h *Handler) HandleReady(w http.ResponseWriter, r *http.Request) {
	if h.Ready == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	if err := h.Ready.Ready(r.Context()); err != nil {
		WriteError(w, http.StatusServiceUnavailable, ErrInternal, "not ready")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// HandleSubmitJob handles POST /v1/jobs.
func (h *Handler) HandleSubmitJob(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}
	// Read the raw body first for debugging
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read request body", "error", err)
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "failed to read request body")
		return
	}
	
	// Log raw body for debugging
	bodyPreview := string(bodyBytes)
	if len(bodyPreview) > 500 {
		bodyPreview = bodyPreview[:500] + "..."
	}
	slog.Info("received raw request body", 
		"body_length", len(bodyBytes),
		"body_preview", bodyPreview)
	
	// Decode from the bytes we just read
	var req SubmitRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		slog.Error("failed to decode request body", "error", err, "body_preview", bodyPreview)
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "invalid JSON body")
		return
	}
	
	// Debug logging for payload validation
	payloadLen := len(req.Payload)
	var payloadPreview string
	if payloadLen > 0 {
		previewLen := payloadLen
		if previewLen > 200 {
			previewLen = 200
		}
		payloadPreview = string(req.Payload[:previewLen])
	} else {
		payloadPreview = "(empty)"
	}
	
	slog.Info("received job submission request", 
		"type", req.Type, 
		"payload_length", payloadLen,
		"payload_preview", payloadPreview,
		"raw_body_has_payload", strings.Contains(string(bodyBytes), `"payload"`))
	
	if req.Type == "" || len(req.Payload) == 0 {
		slog.Warn("validation failed: empty type or payload", 
			"type", req.Type, 
			"payload_length", len(req.Payload),
			"payload_preview", payloadPreview,
			"raw_body_length", len(bodyBytes),
			"raw_body_preview", bodyPreview)
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "type and payload required")
		return
	}
	
	// For assignment-gen and humanizer types, payment is required. For outline-gen and outline-edit, no payment needed.
	if req.Type == "assignment-gen" || req.Type == "humanizer" {
		// Verify payment before creating job
		if req.PaymentOrderID == "" {
			WriteError(w, http.StatusPaymentRequired, ErrPaymentRequired, "payment_order_id is required")
			return
		}
		
		payment, err := h.PaymentStore.GetPaymentByOrderID(r.Context(), req.PaymentOrderID)
		if err != nil {
			slog.Error("get payment failed", "error", err)
			WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to verify payment")
			return
		}
		if payment == nil {
			WriteError(w, http.StatusNotFound, ErrPaymentFailed, "payment not found")
			return
		}
		if payment.UserID != userID {
			WriteError(w, http.StatusForbidden, ErrForbidden, "payment does not belong to user")
			return
		}
		if payment.Status != "completed" {
			WriteError(w, http.StatusPaymentRequired, ErrPaymentRequired, fmt.Sprintf("payment status is %s, must be completed", payment.Status))
			return
		}
		
		// Check if payment is already linked to a job (prevent reuse)
		if payment.JobID != nil {
			WriteError(w, http.StatusBadRequest, ErrPaymentFailed, "payment has already been used for another job")
			return
		}
	}
	
	// Merge outline into payload if provided
	payload := req.Payload
	if len(req.Outline) > 0 && req.Type == "assignment-gen" {
		var payloadMap map[string]interface{}
		if err := json.Unmarshal(req.Payload, &payloadMap); err == nil {
			payloadMap["outline"] = json.RawMessage(req.Outline)
			payload, _ = json.Marshal(payloadMap)
		}
	}
	
	idemKey := r.Header.Get("Idempotency-Key")

	// Log payload before passing to workflow service
	slog.Info("calling workflow.SubmitJob", 
		"type", req.Type,
		"payload_length", len(payload),
		"payload_preview", func() string {
			if len(payload) > 200 {
				return string(payload[:200])
			}
			return string(payload)
		}())

	wfReq := &workflow.SubmitJobRequest{
		UserID:         userID,
		IdempotencyKey: idemKey,
		Type:           req.Type,
		Payload:        payload,
	}
	res, err := h.Workflow.SubmitJob(r.Context(), wfReq)
	if err != nil {
		h.writeWorkflowError(w, err)
		return
	}
	
	// Link payment to job after job is created (for assignment-gen and humanizer)
	if req.Type == "assignment-gen" || req.Type == "humanizer" {
		payment, _ := h.PaymentStore.GetPaymentByOrderID(r.Context(), req.PaymentOrderID)
		if payment != nil {
			jobID, err := uuid.Parse(res.JobID)
			if err == nil {
				if linkErr := h.PaymentStore.LinkPaymentToJob(r.Context(), payment.ID, jobID); linkErr != nil {
					slog.Warn("failed to link payment to job", "payment_id", payment.ID, "job_id", res.JobID, "error", linkErr)
					// Don't fail the request, payment is already verified
				}
			}
		}
	}
	
	out := SubmitResponse{
		JobID:     res.JobID,
		Status:    res.Status,
		CreatedAt: res.CreatedAt,
		Result:    res.Result,
	}
	if res.IsReplay {
		WriteJSON(w, http.StatusOK, out)
		return
	}
	WriteJSON(w, http.StatusAccepted, out)
}

// HandleGetJob handles GET /v1/jobs/:id.
func (h *Handler) HandleGetJob(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		WriteError(w, http.StatusBadRequest, ErrValidationFailed, "job id required")
		return
	}
	res, err := h.Workflow.GetJob(r.Context(), id, userID)
	if err != nil {
		h.writeWorkflowError(w, err)
		return
	}
		out := JobResponseJSON{
			JobID:     res.JobID,
			Type:      res.Type,
			Status:    res.Status,
			CreatedAt: res.CreatedAt,
			UpdatedAt: res.UpdatedAt,
			Result:    res.Result,
		}
	if res.Error != nil {
		out.Error = *res.Error
	}
	WriteJSON(w, http.StatusOK, out)
}

// HandleListJobs handles GET /v1/jobs.
func (h *Handler) HandleListJobs(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}
	
	// Parse query parameters
	jobType := r.URL.Query().Get("type")
	limit := 50 // default
	offset := 0 // default
	
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := fmt.Sscanf(limitStr, "%d", &limit); err != nil || parsed != 1 {
			WriteError(w, http.StatusBadRequest, ErrValidationFailed, "invalid limit parameter")
			return
		}
	}
	
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if parsed, err := fmt.Sscanf(offsetStr, "%d", &offset); err != nil || parsed != 1 {
			WriteError(w, http.StatusBadRequest, ErrValidationFailed, "invalid offset parameter")
			return
		}
	}
	
	jobs, err := h.Workflow.ListJobs(r.Context(), userID, jobType, limit, offset)
	if err != nil {
		slog.Error("list jobs failed", "error", err, "user_id", userID)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to list jobs")
		return
	}
	
	// Convert to JSON response format
	responses := make([]JobResponseJSON, 0, len(jobs))
	for _, job := range jobs {
		out := JobResponseJSON{
			JobID:     job.JobID,
			Type:      job.Type,
			Status:    job.Status,
			CreatedAt: job.CreatedAt,
			UpdatedAt: job.UpdatedAt,
			Result:    job.Result,
		}
		if job.Error != nil {
			out.Error = *job.Error
		}
		responses = append(responses, out)
	}
	
	WriteJSON(w, http.StatusOK, responses)
}

// HandleGenerateOutline handles POST /v1/outlines/generate.
// Generates an outline from assignment description (free, no payment required).
func (h *Handler) HandleGenerateOutline(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}
	var req OutlineGenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "invalid JSON body")
		return
	}
	if req.Type == "" || len(req.Payload) == 0 {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "type and payload required")
		return
	}
	
	idemKey := r.Header.Get("Idempotency-Key")
	
	wfReq := &workflow.SubmitJobRequest{
		UserID:         userID,
		IdempotencyKey: idemKey,
		Type:           "outline-gen",
		Payload:        req.Payload,
	}
	res, err := h.Workflow.SubmitJob(r.Context(), wfReq)
	if err != nil {
		h.writeWorkflowError(w, err)
		return
	}
	
	out := OutlineGenerateResponse{
		JobID:  res.JobID,
		Status: res.Status,
	}
	if res.Result != nil {
		// Result is already unmarshaled as any, try to extract outline
		if resultMap, ok := res.Result.(map[string]interface{}); ok {
			if outline, ok := resultMap["outline"]; ok {
				out.Outline = outline
			}
		} else if resultBytes, ok := res.Result.([]byte); ok {
			// If it's still bytes, unmarshal it
			var resultMap map[string]interface{}
			if err := json.Unmarshal(resultBytes, &resultMap); err == nil {
				if outline, ok := resultMap["outline"]; ok {
					out.Outline = outline
				}
			}
		}
	}
	
	if res.IsReplay {
		WriteJSON(w, http.StatusOK, out)
		return
	}
	WriteJSON(w, http.StatusAccepted, out)
}

// HandleEditOutline handles POST /v1/outlines/edit.
// Edits an outline based on user chat message (free, no payment required).
func (h *Handler) HandleEditOutline(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}
	var req OutlineEditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "invalid JSON body")
		return
	}
	if len(req.Outline) == 0 || req.UserMessage == "" {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "outline and user_message are required")
		return
	}
	
	// Create payload with outline and user message
	payload := map[string]interface{}{
		"outline":      json.RawMessage(req.Outline),
		"user_message": req.UserMessage,
	}
	payloadBytes, _ := json.Marshal(payload)
	
	idemKey := r.Header.Get("Idempotency-Key")
	
	wfReq := &workflow.SubmitJobRequest{
		UserID:         userID,
		IdempotencyKey: idemKey,
		Type:           "outline-edit",
		Payload:        payloadBytes,
	}
	res, err := h.Workflow.SubmitJob(r.Context(), wfReq)
	if err != nil {
		h.writeWorkflowError(w, err)
		return
	}
	
	out := OutlineEditResponse{
		JobID:  res.JobID,
		Status: res.Status,
	}
	if res.Result != nil {
		// Result is already unmarshaled as any, try to extract outline and message
		if resultMap, ok := res.Result.(map[string]interface{}); ok {
			if outline, ok := resultMap["outline"]; ok {
				out.Outline = outline
			}
			if msg, ok := resultMap["assistant_message"].(string); ok {
				out.AssistantMessage = msg
			}
		} else if resultBytes, ok := res.Result.([]byte); ok {
			// If it's still bytes, unmarshal it
			var resultMap map[string]interface{}
			if err := json.Unmarshal(resultBytes, &resultMap); err == nil {
				if outline, ok := resultMap["outline"]; ok {
					out.Outline = outline
				}
				if msg, ok := resultMap["assistant_message"].(string); ok {
					out.AssistantMessage = msg
				}
			}
		}
	}
	
	if res.IsReplay {
		WriteJSON(w, http.StatusOK, out)
		return
	}
	WriteJSON(w, http.StatusAccepted, out)
}

// HandleCreatePaymentOrder handles POST /v1/payments/create-order.
// Creates a Razorpay order and returns order_id and key_id for frontend.
func (h *Handler) HandleCreatePaymentOrder(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}

	var req PaymentCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "invalid JSON body")
		return
	}

	// If job_type is humanizer and payload is provided, calculate price dynamically
	if req.JobType == "humanizer" && len(req.Payload) > 0 {
		wordCount, err := pricing.EstimateWordCountFromPayload(req.Payload)
		if err != nil {
			slog.Warn("failed to estimate word count, using provided amount", "error", err)
		} else {
			calculatedAmount := pricing.CalculateHumanizerPrice(wordCount)
			if req.Amount <= 0 {
				req.Amount = calculatedAmount
			} else if req.Amount < calculatedAmount {
				slog.Warn("provided amount is less than calculated amount, using calculated", "provided", req.Amount, "calculated", calculatedAmount)
				// Use calculated amount for security (prevent underpayment)
				req.Amount = calculatedAmount
			}
		}
	}

	if req.Amount <= 0 {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "amount must be greater than 0")
		return
	}

	// Check if Razorpay key is configured
	if h.RazorpayKey == "" {
		slog.Error("razorpay key not configured")
		WriteError(w, http.StatusInternalServerError, ErrInternal, "payment service not configured")
		return
	}

	slog.Info("creating payment order", "user_id", userID, "amount", req.Amount, "key_id", h.RazorpayKey[:10]+"...")

	// Create Razorpay order server-side
	client := razorpay.NewClient(h.RazorpayKey, h.RazorpaySecret)
	
	orderData := map[string]interface{}{
		"amount":   req.Amount,
		"currency": "INR",
		"receipt":  fmt.Sprintf("studojo_%s_%d", userID[:8], time.Now().Unix()),
	}
	
	razorpayOrder, err := client.Order.Create(orderData, nil)
	if err != nil {
		slog.Error("failed to create razorpay order", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to create payment order")
		return
	}
	
	orderID, ok := razorpayOrder["id"].(string)
	if !ok || orderID == "" {
		slog.Error("invalid order response from razorpay", "response", razorpayOrder)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "invalid order response")
		return
	}
	
	slog.Info("razorpay order created", "order_id", orderID)
	
	WriteJSON(w, http.StatusOK, PaymentCreateResponse{
		OrderID: orderID,
		Amount:  req.Amount,
		KeyID:   h.RazorpayKey,
	})
}

// HandleVerifyPayment handles POST /v1/payments/verify.
// Verifies Razorpay payment signature and updates payment status.
func (h *Handler) HandleVerifyPayment(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}

	var req PaymentVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "invalid JSON body")
		return
	}

	if req.RazorpayOrderID == "" || req.RazorpayPaymentID == "" || req.RazorpaySignature == "" {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "razorpay_order_id, razorpay_payment_id, and razorpay_signature are required")
		return
	}

	// Get payment by order ID (created during verification)
	payment, err := h.PaymentStore.GetPaymentByOrderID(r.Context(), req.RazorpayOrderID)
	if err != nil {
		slog.Error("get payment failed", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to get payment")
		return
	}
	
	// If payment doesn't exist, create it (Razorpay Checkout created order client-side)
	if payment == nil {
		// Create payment record with the order_id from Razorpay
		// Amount should match what was paid - we'll use a standard amount for assignment-gen
		payment = &store.Payment{
			ID:              uuid.New(),
			UserID:          userID,
			JobID:           nil,
			RazorpayOrderID: req.RazorpayOrderID,
			RazorpayPaymentID: nil,
			Amount:          13900, // ₹139 - standard assignment price
			Status:          "pending",
			CreatedAt:       time.Now().UTC(),
			UpdatedAt:       time.Now().UTC(),
		}
		if err := h.PaymentStore.CreatePayment(r.Context(), payment); err != nil {
			slog.Error("create payment failed", "error", err)
			WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to create payment")
			return
		}
	} else {
		// Verify payment belongs to user
		if payment.UserID != userID {
			WriteError(w, http.StatusForbidden, ErrForbidden, "payment does not belong to user")
			return
		}
	}

	// Verify Razorpay signature
	if !h.verifyRazorpaySignature(req.RazorpayOrderID, req.RazorpayPaymentID, req.RazorpaySignature) {
		WriteError(w, http.StatusBadRequest, ErrPaymentFailed, "invalid payment signature")
		return
	}

	// Update payment status to completed
	if err := h.PaymentStore.UpdatePayment(r.Context(), payment.ID, &req.RazorpayPaymentID, "completed"); err != nil {
		slog.Error("update payment failed", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to update payment")
		return
	}

	// If job_id is provided, link payment to job
	var jobIDStr string
	if req.JobID != "" {
		jobID, err := uuid.Parse(req.JobID)
		if err == nil {
			// Update payment with job_id (we'll need to add this method to store)
			// For now, we'll handle this in the job submission flow
			jobIDStr = jobID.String()
		}
	}

	WriteJSON(w, http.StatusOK, PaymentVerifyResponse{
		PaymentID: payment.ID.String(),
		Status:    "completed",
		JobID:     jobIDStr,
	})
}

// verifyRazorpaySignature verifies Razorpay payment signature using HMAC SHA256.
func (h *Handler) verifyRazorpaySignature(orderID, paymentID, signature string) bool {
	if h.RazorpaySecret == "" {
		slog.Warn("razorpay secret not configured, skipping signature verification")
		return true // Allow in development if secret not set
	}

	// Create message: order_id + "|" + payment_id
	message := orderID + "|" + paymentID

	// Compute HMAC SHA256
	mac := hmac.New(sha256.New, []byte(h.RazorpaySecret))
	mac.Write([]byte(message))
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	// Compare signatures (constant-time comparison)
	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}

func (h *Handler) writeWorkflowError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workflow.ErrNotFound):
		WriteError(w, http.StatusNotFound, ErrJobNotFound, "job not found")
	case errors.Is(err, workflow.ErrForbidden):
		WriteError(w, http.StatusForbidden, ErrForbidden, "forbidden")
	case errors.Is(err, workflow.ErrConflict):
		WriteError(w, http.StatusConflict, ErrInvalidIdempotencyKey, "idempotency key already used by another user")
	case errors.Is(err, workflow.ErrValidation):
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "validation failed")
	default:
		slog.Error("workflow error", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "internal error")
	}
}
