package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// AdminHandler handles admin-only endpoints.
type AdminHandler struct {
	DB *sql.DB
}

// User represents a user in admin responses.
type AdminUser struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Email             string    `json:"email"`
	EmailVerified     bool      `json:"email_verified"`
	PhoneNumber       *string   `json:"phone_number,omitempty"`
	PhoneNumberVerified bool   `json:"phone_number_verified"`
	Role              *string   `json:"role,omitempty"`
	Banned            *bool      `json:"banned,omitempty"`
	BanReason         *string    `json:"ban_reason,omitempty"`
	BanExpires        *time.Time `json:"ban_expires,omitempty"`
	FullName          *string    `json:"full_name,omitempty"`
	College           *string    `json:"college,omitempty"`
	YearOfStudy       *string    `json:"year_of_study,omitempty"`
	Course            *string    `json:"course,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// DissertationSubmission represents a dissertation submission.
type DissertationSubmission struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	Email             string                 `json:"email"`
	PhoneNumber       string                 `json:"phone_number"`
	DissertationTitle string                 `json:"dissertation_title"`
	DataType          string                 `json:"data_type"`
	CurrentStage      string                 `json:"current_stage"`
	AdditionalNotes   *string                `json:"additional_notes,omitempty"`
	FormData          map[string]interface{}  `json:"form_data,omitempty"`
	RazorpayOrderID   *string                `json:"razorpay_order_id,omitempty"`
	RazorpayPaymentID *string                `json:"razorpay_payment_id,omitempty"`
	PaymentStatus     string                 `json:"payment_status"`
	Amount            int                    `json:"amount"`
	Status            string                 `json:"status"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

// CareerApplication represents a career application.
type CareerApplication struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	Email             string                 `json:"email"`
	PhoneNumber       string                 `json:"phone_number"`
	City              string                 `json:"city"`
	InstitutionName   string                 `json:"institution_name"`
	CurrentYear       string                 `json:"current_year"`
	Course            string                 `json:"course"`
	AreasOfInterest   []interface{}          `json:"areas_of_interest"`
	FormData          map[string]interface{}  `json:"form_data,omitempty"`
	RazorpayOrderID   *string                `json:"razorpay_order_id,omitempty"`
	RazorpayPaymentID *string                `json:"razorpay_payment_id,omitempty"`
	PaymentStatus     string                 `json:"payment_status"`
	Amount            int                    `json:"amount"`
	Status            string                 `json:"status"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

// HandleListUsers handles GET /v1/admin/users.
func (h *AdminHandler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil {
			offset = parsed
		}
	}

	search := r.URL.Query().Get("search")
	searchPattern := "%" + search + "%"

	var rows *sql.Rows
	var err error

	query := `
		SELECT u.id, u.name, u.email, u.email_verified, u.phone_number, u.phone_number_verified,
		       u.role, u.banned, u.ban_reason, u.ban_expires, u.created_at, u.updated_at,
		       up.full_name, up.college, up.year_of_study, up.course
		FROM "user" u
		LEFT JOIN user_profile up ON u.id = up.user_id
	`

	args := []interface{}{}
	argIdx := 1

	if search != "" {
		query += ` WHERE (
			u.name ILIKE $` + strconv.Itoa(argIdx) + ` OR
			u.email ILIKE $` + strconv.Itoa(argIdx) + ` OR
			u.phone_number ILIKE $` + strconv.Itoa(argIdx) + ` OR
			up.full_name ILIKE $` + strconv.Itoa(argIdx) + ` OR
			up.college ILIKE $` + strconv.Itoa(argIdx) + ` OR
			up.course ILIKE $` + strconv.Itoa(argIdx) + `
		)`
		args = append(args, searchPattern)
		argIdx++
	}

	query += ` ORDER BY u.created_at DESC LIMIT $` + strconv.Itoa(argIdx) + ` OFFSET $` + strconv.Itoa(argIdx+1)
	args = append(args, limit, offset)

	rows, err = h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to list users", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to list users")
		return
	}
	defer rows.Close()

	var users []AdminUser
	for rows.Next() {
		var u AdminUser
		var phoneNumber, role, banReason sql.NullString
		var banExpires sql.NullTime
		var banned sql.NullBool
		var fullName, college, yearOfStudy, course sql.NullString

		err := rows.Scan(
			&u.ID, &u.Name, &u.Email, &u.EmailVerified,
			&phoneNumber, &u.PhoneNumberVerified,
			&role, &banned, &banReason, &banExpires,
			&u.CreatedAt, &u.UpdatedAt,
			&fullName, &college, &yearOfStudy, &course,
		)
		if err != nil {
			slog.Error("failed to scan user", "error", err)
			continue
		}

		if phoneNumber.Valid {
			u.PhoneNumber = &phoneNumber.String
		}
		if role.Valid {
			u.Role = &role.String
		}
		if banned.Valid {
			u.Banned = &banned.Bool
		}
		if banReason.Valid {
			u.BanReason = &banReason.String
		}
		if banExpires.Valid {
			u.BanExpires = &banExpires.Time
		}
		if fullName.Valid {
			u.FullName = &fullName.String
		}
		if college.Valid {
			u.College = &college.String
		}
		if yearOfStudy.Valid {
			u.YearOfStudy = &yearOfStudy.String
		}
		if course.Valid {
			u.Course = &course.String
		}

		users = append(users, u)
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"users": users,
		"limit": limit,
		"offset": offset,
	})
}

// HandleGetUser handles GET /v1/admin/users/:id.
func (h *AdminHandler) HandleGetUser(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if userID == "" {
		WriteError(w, http.StatusBadRequest, ErrValidationFailed, "user id required")
		return
	}

	var u AdminUser
	var phoneNumber, role, banReason sql.NullString
	var banExpires sql.NullTime
	var banned sql.NullBool
	var fullName, college, yearOfStudy, course sql.NullString

	err := h.DB.QueryRowContext(r.Context(), `
		SELECT u.id, u.name, u.email, u.email_verified, u.phone_number, u.phone_number_verified,
		       u.role, u.banned, u.ban_reason, u.ban_expires, u.created_at, u.updated_at,
		       up.full_name, up.college, up.year_of_study, up.course
		FROM "user" u
		LEFT JOIN user_profile up ON u.id = up.user_id
		WHERE u.id = $1`,
		userID,
	).Scan(
		&u.ID, &u.Name, &u.Email, &u.EmailVerified,
		&phoneNumber, &u.PhoneNumberVerified,
		&role, &banned, &banReason, &banExpires,
		&u.CreatedAt, &u.UpdatedAt,
		&fullName, &college, &yearOfStudy, &course,
	)
	if err == sql.ErrNoRows {
		WriteError(w, http.StatusNotFound, ErrNotFound, "user not found")
		return
	}
	if err != nil {
		slog.Error("failed to get user", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to get user")
		return
	}

	if phoneNumber.Valid {
		u.PhoneNumber = &phoneNumber.String
	}
	if role.Valid {
		u.Role = &role.String
	}
	if banned.Valid {
		u.Banned = &banned.Bool
	}
	if banReason.Valid {
		u.BanReason = &banReason.String
	}
	if banExpires.Valid {
		u.BanExpires = &banExpires.Time
	}
	if fullName.Valid {
		u.FullName = &fullName.String
	}
	if college.Valid {
		u.College = &college.String
	}
	if yearOfStudy.Valid {
		u.YearOfStudy = &yearOfStudy.String
	}
	if course.Valid {
		u.Course = &course.String
	}

	WriteJSON(w, http.StatusOK, u)
}

// HandleUpdateUser handles PATCH /v1/admin/users/:id.
func (h *AdminHandler) HandleUpdateUser(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if userID == "" {
		WriteError(w, http.StatusBadRequest, ErrValidationFailed, "user id required")
		return
	}

	var req struct {
		Role      *string    `json:"role,omitempty"`
		Banned    *bool      `json:"banned,omitempty"`
		BanReason *string    `json:"ban_reason,omitempty"`
		BanExpires *time.Time `json:"ban_expires,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "invalid JSON body")
		return
	}

	// Whitelist of allowed field names for UPDATE queries
	// This prevents SQL injection if field names are ever derived from user input
	allowedFields := map[string]bool{
		"role":        true,
		"banned":      true,
		"ban_reason":  true,
		"ban_expires": true,
		"updated_at":  true,
	}

	// Build update query dynamically with whitelist validation
	updates := []string{"updated_at = $1"}
	args := []interface{}{time.Now().UTC()}
	argIdx := 2

	// Helper function to safely add field updates
	addFieldUpdate := func(fieldName string, value interface{}) {
		// Validate field name against whitelist
		if !allowedFields[fieldName] {
			slog.Warn("attempted to update non-whitelisted field", "field", fieldName)
			return // Skip non-whitelisted fields
		}
		updates = append(updates, fieldName+" = $"+strconv.Itoa(argIdx))
		args = append(args, value)
		argIdx++
	}

	if req.Role != nil {
		addFieldUpdate("role", *req.Role)
	}
	if req.Banned != nil {
		addFieldUpdate("banned", *req.Banned)
	}
	if req.BanReason != nil {
		addFieldUpdate("ban_reason", *req.BanReason)
	}
	if req.BanExpires != nil {
		addFieldUpdate("ban_expires", *req.BanExpires)
	}

	if len(updates) == 1 {
		WriteError(w, http.StatusBadRequest, ErrValidationFailed, "no fields to update")
		return
	}

	args = append(args, userID)
	query := `UPDATE "user" SET ` + updates[0]
	for i := 1; i < len(updates); i++ {
		query += ", " + updates[i]
	}
	query += " WHERE id = $" + strconv.Itoa(argIdx)

	_, err := h.DB.ExecContext(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to update user", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to update user")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// HandleListDissertations handles GET /v1/admin/dissertations.
func (h *AdminHandler) HandleListDissertations(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil {
			offset = parsed
		}
	}

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id, name, email, phone_number, dissertation_title, data_type, current_stage,
		       additional_notes, form_data, razorpay_order_id, razorpay_payment_id,
		       payment_status, amount, status, created_at, updated_at
		FROM "dissertation_submissions"
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		slog.Warn("failed to list dissertations (table may not exist)", "error", err)
		// Return empty list if table doesn't exist
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"submissions": []DissertationSubmission{},
			"limit":       limit,
			"offset":      offset,
			"total":       0,
		})
		return
	}
	defer rows.Close()

	var submissions []DissertationSubmission
	for rows.Next() {
		var d DissertationSubmission
		var additionalNotes, razorpayOrderID, razorpayPaymentID sql.NullString
		var formData []byte

		err := rows.Scan(
			&d.ID, &d.Name, &d.Email, &d.PhoneNumber,
			&d.DissertationTitle, &d.DataType, &d.CurrentStage,
			&additionalNotes, &formData,
			&razorpayOrderID, &razorpayPaymentID,
			&d.PaymentStatus, &d.Amount, &d.Status,
			&d.CreatedAt, &d.UpdatedAt,
		)
		if err != nil {
			slog.Error("failed to scan dissertation", "error", err)
			continue
		}

		if additionalNotes.Valid {
			d.AdditionalNotes = &additionalNotes.String
		}
		if razorpayOrderID.Valid {
			d.RazorpayOrderID = &razorpayOrderID.String
		}
		if razorpayPaymentID.Valid {
			d.RazorpayPaymentID = &razorpayPaymentID.String
		}
		if len(formData) > 0 {
			json.Unmarshal(formData, &d.FormData)
		}

		submissions = append(submissions, d)
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"submissions": submissions,
		"limit": limit,
		"offset": offset,
	})
}

// HandleListCareers handles GET /v1/admin/careers.
func (h *AdminHandler) HandleListCareers(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil {
			offset = parsed
		}
	}

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id, name, email, phone_number, city, institution_name, current_year, course,
		       areas_of_interest, form_data, razorpay_order_id, razorpay_payment_id,
		       payment_status, amount, status, created_at, updated_at
		FROM "career_applications"
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		slog.Warn("failed to list careers (table may not exist)", "error", err)
		// Return empty list if table doesn't exist
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"applications": []CareerApplication{},
			"limit":        limit,
			"offset":       offset,
			"total":        0,
		})
		return
	}
	defer rows.Close()

	var applications []CareerApplication
	for rows.Next() {
		var c CareerApplication
		var razorpayOrderID, razorpayPaymentID sql.NullString
		var areasOfInterest, formData []byte

		err := rows.Scan(
			&c.ID, &c.Name, &c.Email, &c.PhoneNumber,
			&c.City, &c.InstitutionName, &c.CurrentYear, &c.Course,
			&areasOfInterest, &formData,
			&razorpayOrderID, &razorpayPaymentID,
			&c.PaymentStatus, &c.Amount, &c.Status,
			&c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			slog.Error("failed to scan career application", "error", err)
			continue
		}

		if razorpayOrderID.Valid {
			c.RazorpayOrderID = &razorpayOrderID.String
		}
		if razorpayPaymentID.Valid {
			c.RazorpayPaymentID = &razorpayPaymentID.String
		}
		if len(areasOfInterest) > 0 {
			json.Unmarshal(areasOfInterest, &c.AreasOfInterest)
		}
		if len(formData) > 0 {
			json.Unmarshal(formData, &c.FormData)
		}

		applications = append(applications, c)
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"applications": applications,
		"limit": limit,
		"offset": offset,
	})
}

// HandleGetJob handles GET /v1/admin/jobs/:id.
// Allows admins to fetch any job by ID (bypassing ownership check).
func (h *AdminHandler) HandleGetJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		WriteError(w, http.StatusBadRequest, ErrValidationFailed, "job id required")
		return
	}

	var j struct {
		JobID     string    `json:"job_id"`
		UserID    string    `json:"user_id"`
		Type      string    `json:"type"`
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		Result    []byte    `json:"-"`
		ResultObj any       `json:"result,omitempty"`
		Error     *string   `json:"error,omitempty"`
	}

	err := h.DB.QueryRowContext(r.Context(), `
		SELECT id, user_id, type, status, created_at, updated_at, result, error
		FROM cp.jobs
		WHERE id = $1`,
		jobID,
	).Scan(
		&j.JobID, &j.UserID, &j.Type, &j.Status, &j.CreatedAt, &j.UpdatedAt, &j.Result, &j.Error,
	)
	if err == sql.ErrNoRows {
		WriteError(w, http.StatusNotFound, ErrNotFound, "job not found")
		return
	}
	if err != nil {
		slog.Error("failed to get job", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to get job")
		return
	}

	if len(j.Result) > 0 {
		var r any
		if err := json.Unmarshal(j.Result, &r); err == nil {
			j.ResultObj = r
		}
	}

	WriteJSON(w, http.StatusOK, j)
}

// MonthMetric represents metrics for a single month.
type MonthMetric struct {
	Month            string `json:"month"`              // Format: "YYYY-MM"
	UsersCount       int64  `json:"users_count"`
	OrdersCount      int64  `json:"orders_count"`
	DissertationsCount int64  `json:"dissertations_count"`
	Revenue          int64  `json:"revenue"`            // In paise
}

// AssignmentOrder represents an assignment order with payment info.
type AssignmentOrder struct {
	JobID       string `json:"job_id"`
	UserID      string `json:"user_id"`
	UserName    string `json:"user_name"`
	CreatedAt   string `json:"created_at"`
	Status      string `json:"status"`
	Amount      int64  `json:"amount"`       // In paise
	DownloadURL string `json:"download_url,omitempty"`
}

// RevenueBreakdown represents revenue split by type.
type RevenueBreakdown struct {
	Total        int64 `json:"total"`         // In paise
	Assignments  int64 `json:"assignments"`   // In paise
	Dissertations int64 `json:"dissertations"` // In paise
	Careers      int64 `json:"careers"`       // In paise
}

// DashboardStats represents dashboard statistics.
type DashboardStats struct {
	TotalUsers        int64 `json:"total_users"`
	TotalDissertations int64 `json:"total_dissertations"`
	TotalCareers      int64 `json:"total_careers"`
	UsersLast7Days    int64 `json:"users_last_7_days"`
	UsersLast30Days   int64 `json:"users_last_30_days"`
	DissertationsLast7Days  int64 `json:"dissertations_last_7_days"`
	DissertationsLast30Days int64 `json:"dissertations_last_30_days"`
	CareersLast7Days   int64 `json:"careers_last_7_days"`
	CareersLast30Days  int64 `json:"careers_last_30_days"`
	BannedUsers       int64 `json:"banned_users"`
	ActiveUsers       int64 `json:"active_users"`
	CompletedPayments int64 `json:"completed_payments"`
	PendingPayments   int64 `json:"pending_payments"`
	MonthlyMetrics   []MonthMetric `json:"monthly_metrics"`
	AssignmentOrders []AssignmentOrder `json:"assignment_orders"`
	RevenueBreakdown RevenueBreakdown `json:"revenue_breakdown"`
}

// HandleGetDashboardStats handles GET /v1/admin/stats.
func (h *AdminHandler) HandleGetDashboardStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stats := DashboardStats{}

	// Total users
	err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM "user"`).Scan(&stats.TotalUsers)
	if err != nil {
		slog.Error("failed to get total users", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to get stats")
		return
	}

	// Total dissertations (handle case where table might not exist)
	err = h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM "dissertation_submissions"`).Scan(&stats.TotalDissertations)
	if err != nil {
		slog.Warn("failed to get total dissertations (table may not exist)", "error", err)
		stats.TotalDissertations = 0
	}

	// Total careers (handle case where table might not exist)
	err = h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM "career_applications"`).Scan(&stats.TotalCareers)
	if err != nil {
		slog.Warn("failed to get total careers (table may not exist)", "error", err)
		stats.TotalCareers = 0
	}

	// Users last 7 days
	err = h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM "user"
		WHERE created_at >= NOW() - INTERVAL '7 days'
	`).Scan(&stats.UsersLast7Days)
	if err != nil {
		slog.Error("failed to get users last 7 days", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to get stats")
		return
	}

	// Users last 30 days
	err = h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM "user"
		WHERE created_at >= NOW() - INTERVAL '30 days'
	`).Scan(&stats.UsersLast30Days)
	if err != nil {
		slog.Error("failed to get users last 30 days", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to get stats")
		return
	}

	// Dissertations last 7 days
	err = h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM "dissertation_submissions"
		WHERE created_at >= NOW() - INTERVAL '7 days'
	`).Scan(&stats.DissertationsLast7Days)
	if err != nil {
		slog.Warn("failed to get dissertations last 7 days (table may not exist)", "error", err)
		stats.DissertationsLast7Days = 0
	}

	// Dissertations last 30 days
	err = h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM "dissertation_submissions"
		WHERE created_at >= NOW() - INTERVAL '30 days'
	`).Scan(&stats.DissertationsLast30Days)
	if err != nil {
		slog.Warn("failed to get dissertations last 30 days (table may not exist)", "error", err)
		stats.DissertationsLast30Days = 0
	}

	// Careers last 7 days
	err = h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM "career_applications"
		WHERE created_at >= NOW() - INTERVAL '7 days'
	`).Scan(&stats.CareersLast7Days)
	if err != nil {
		slog.Warn("failed to get careers last 7 days (table may not exist)", "error", err)
		stats.CareersLast7Days = 0
	}

	// Careers last 30 days
	err = h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM "career_applications"
		WHERE created_at >= NOW() - INTERVAL '30 days'
	`).Scan(&stats.CareersLast30Days)
	if err != nil {
		slog.Warn("failed to get careers last 30 days (table may not exist)", "error", err)
		stats.CareersLast30Days = 0
	}

	// Banned users
	err = h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM "user"
		WHERE banned = true
	`).Scan(&stats.BannedUsers)
	if err != nil {
		slog.Error("failed to get banned users", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to get stats")
		return
	}

	// Active users (not banned)
	err = h.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM "user"
		WHERE banned IS NULL OR banned = false
	`).Scan(&stats.ActiveUsers)
	if err != nil {
		slog.Error("failed to get active users", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to get stats")
		return
	}

	// Completed payments (from dissertations and careers)
	// Use COALESCE to handle case where tables don't exist
	err = h.DB.QueryRowContext(ctx, `
		SELECT COALESCE((
			SELECT COUNT(*) FROM "dissertation_submissions" WHERE payment_status = 'completed'
		), 0) + COALESCE((
			SELECT COUNT(*) FROM "career_applications" WHERE payment_status = 'completed'
		), 0)
	`).Scan(&stats.CompletedPayments)
	if err != nil {
		slog.Warn("failed to get completed payments (tables may not exist)", "error", err)
		stats.CompletedPayments = 0
	}

	// Pending payments
	err = h.DB.QueryRowContext(ctx, `
		SELECT COALESCE((
			SELECT COUNT(*) FROM "dissertation_submissions" WHERE payment_status != 'completed'
		), 0) + COALESCE((
			SELECT COUNT(*) FROM "career_applications" WHERE payment_status != 'completed'
		), 0)
	`).Scan(&stats.PendingPayments)
	if err != nil {
		slog.Warn("failed to get pending payments (tables may not exist)", "error", err)
		stats.PendingPayments = 0
	}

	// Monthly metrics (last 12 months)
	rows, err := h.DB.QueryContext(ctx, `
		WITH months AS (
			SELECT to_char(generate_series(
				date_trunc('month', NOW() - INTERVAL '11 months'),
				date_trunc('month', NOW()),
				'1 month'::interval
			), 'YYYY-MM') AS month
		)
		SELECT 
			m.month,
			COALESCE(COUNT(DISTINCT u.id), 0) AS users_count,
			COALESCE(COUNT(DISTINCT j.id), 0) AS orders_count,
			COALESCE(COUNT(DISTINCT d.id), 0) AS dissertations_count,
			COALESCE((
				SELECT SUM(p2.amount)
				FROM cp.payments p2
				INNER JOIN cp.jobs j2 ON p2.job_id = j2.id
				WHERE j2.type = 'assignment-gen'
					AND p2.status = 'completed'
					AND to_char(p2.created_at, 'YYYY-MM') = m.month
			), 0) AS revenue
		FROM months m
		LEFT JOIN "user" u ON to_char(u.created_at, 'YYYY-MM') = m.month
		LEFT JOIN cp.jobs j ON to_char(j.created_at, 'YYYY-MM') = m.month AND j.type = 'assignment-gen'
		LEFT JOIN "dissertation_submissions" d ON to_char(d.created_at, 'YYYY-MM') = m.month
		GROUP BY m.month
		ORDER BY m.month
	`)
	if err != nil {
		slog.Warn("failed to get monthly metrics", "error", err)
		stats.MonthlyMetrics = []MonthMetric{}
	} else {
		defer rows.Close()
		stats.MonthlyMetrics = []MonthMetric{}
		for rows.Next() {
			var m MonthMetric
			err := rows.Scan(&m.Month, &m.UsersCount, &m.OrdersCount, &m.DissertationsCount, &m.Revenue)
			if err != nil {
				slog.Error("failed to scan monthly metric", "error", err)
				continue
			}
			stats.MonthlyMetrics = append(stats.MonthlyMetrics, m)
		}
	}

	// Assignment orders (last 50, with payment info)
	rows, err = h.DB.QueryContext(ctx, `
		SELECT 
			j.id::text,
			j.user_id,
			COALESCE(u.name, 'Unknown') AS user_name,
			j.created_at,
			j.status,
			COALESCE(p.amount, 0) AS amount
		FROM cp.jobs j
		LEFT JOIN "user" u ON u.id = j.user_id
		LEFT JOIN cp.payments p ON p.job_id = j.id
		WHERE j.type = 'assignment-gen'
		ORDER BY j.created_at DESC
		LIMIT 50
	`)
	if err != nil {
		slog.Warn("failed to get assignment orders", "error", err)
		stats.AssignmentOrders = []AssignmentOrder{}
	} else {
		defer rows.Close()
		stats.AssignmentOrders = []AssignmentOrder{}
		for rows.Next() {
			var o AssignmentOrder
			var createdAt time.Time
			err := rows.Scan(&o.JobID, &o.UserID, &o.UserName, &createdAt, &o.Status, &o.Amount)
			if err != nil {
				slog.Error("failed to scan assignment order", "error", err)
				continue
			}
			o.CreatedAt = createdAt.Format(time.RFC3339)
			if o.Status == "COMPLETED" {
				// Generate download URL (assuming API endpoint pattern)
				o.DownloadURL = fmt.Sprintf("/v1/jobs/%s", o.JobID)
			}
			stats.AssignmentOrders = append(stats.AssignmentOrders, o)
		}
	}

	// Revenue breakdown
	var assignmentsRev, dissertationsRev, careersRev int64

	// Assignments revenue (from cp.payments where job_id is not null)
	err = h.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM cp.payments
		WHERE job_id IS NOT NULL AND status = 'completed'
	`).Scan(&assignmentsRev)
	if err != nil {
		slog.Error("failed to get assignments revenue", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to get stats")
		return
	}

	// Dissertations revenue
	err = h.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM "dissertation_submissions"
		WHERE payment_status = 'completed'
	`).Scan(&dissertationsRev)
	if err != nil {
		slog.Warn("failed to get dissertations revenue (table may not exist)", "error", err)
		dissertationsRev = 0
	}

	// Careers revenue
	err = h.DB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM "career_applications"
		WHERE payment_status = 'completed'
	`).Scan(&careersRev)
	if err != nil {
		slog.Warn("failed to get careers revenue (table may not exist)", "error", err)
		careersRev = 0
	}

	stats.RevenueBreakdown = RevenueBreakdown{
		Assignments:  assignmentsRev,
		Dissertations: dissertationsRev,
		Careers:      careersRev,
		Total:        assignmentsRev + dissertationsRev + careersRev,
	}

	WriteJSON(w, http.StatusOK, stats)
}

