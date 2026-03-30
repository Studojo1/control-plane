package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/studojo/control-plane/internal/api"
	"github.com/studojo/control-plane/internal/auth"
	"github.com/studojo/control-plane/internal/k8s"
	"github.com/studojo/control-plane/internal/messaging"
	"github.com/studojo/control-plane/internal/ready"
	"github.com/studojo/control-plane/internal/store"
	"github.com/studojo/control-plane/internal/workflow"
)

// ensureSSLMode appends sslmode=disable to the DSN if no sslmode is set.
// Use when connecting to Postgres that has SSL disabled (e.g. local Docker).
func ensureSSLMode(dsn string) string {
	if strings.Contains(strings.ToLower(dsn), "sslmode=") {
		return dsn
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&sslmode=disable"
	}
	return dsn + "?sslmode=disable"
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgresql://studojo:studojo@localhost:5432/postgres?sslmode=disable"
	}
	dbURL = ensureSSLMode(dbURL)
	rabbitURL := os.Getenv("RABBITMQ_URL")
	if rabbitURL == "" {
		rabbitURL = "amqp://guest:guest@localhost:5672/"
	}
	jwksURL := os.Getenv("JWKS_URL")
	if jwksURL == "" {
		jwksURL = "http://localhost:3000/api/auth/jwks"
	}
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8080"
	}
	corsOrigins := strings.Split(os.Getenv("CORS_ORIGINS"), ",")
	if len(corsOrigins) == 1 && strings.TrimSpace(corsOrigins[0]) == "" {
		// Default to allow both frontend and admin panel in development
		corsOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000", "http://localhost:3001", "http://127.0.0.1:3001"}
	}

	if err := store.Migrate(dbURL); err != nil {
		slog.Error("migrate failed", "error", err)
		os.Exit(1)
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		slog.Error("db open failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		slog.Error("db ping failed", "error", err)
		os.Exit(1)
	}

	msgCfg := messaging.DefaultConfig(rabbitURL)
	if err := messaging.EnsureAssignmentGenQueue(msgCfg); err != nil {
		slog.Warn("ensure assignment-gen queue failed (workers may create it)", "error", err)
	}
	if err := messaging.EnsureResumeQueue(msgCfg); err != nil {
		slog.Warn("ensure resume queue failed (workers may create it)", "error", err)
	}
	pub, err := messaging.NewRabbitPublisher(msgCfg)
	if err != nil {
		slog.Error("rabbit publisher failed", "error", err)
		os.Exit(1)
	}
	defer pub.Close()

	st := store.NewPostgresStore(db)
	wf := &workflow.Service{Store: st, Publisher: pub}
	readyChecker := &ready.Checker{DB: db, RabbitMQURL: rabbitURL}

	// Razorpay configuration: support test/prod mode switching
	razorpayMode := os.Getenv("RAZORPAY_MODE")
	if razorpayMode == "" {
		razorpayMode = "test" // Default to test mode
	}

	var razorpayKey, razorpaySecret string
	if razorpayMode == "prod" || razorpayMode == "production" {
		razorpayKey = os.Getenv("RAZORPAY_KEY_PROD_ID")
		razorpaySecret = os.Getenv("RAZORPAY_KEY_PROD_SECRET")
		slog.Info("Razorpay mode: PRODUCTION")
	} else {
		razorpayKey = os.Getenv("RAZORPAY_KEY_TEST_ID")
		razorpaySecret = os.Getenv("RAZORPAY_KEY_TEST_SECRET")
		slog.Info("Razorpay mode: TEST")
	}

	if razorpayKey == "" {
		slog.Warn("Razorpay key not set - payment functionality will not work", "mode", razorpayMode)
	} else {
		keyPreview := razorpayKey
		if len(keyPreview) > 10 {
			keyPreview = keyPreview[:10]
		}
		slog.Info("Razorpay keys loaded", "key_id", keyPreview+"...", "mode", razorpayMode)
	}
	if razorpaySecret == "" {
		slog.Warn("Razorpay secret not set - payment signature verification will not work", "mode", razorpayMode)
	}

	h := &api.Handler{
		Workflow:       wf,
		Ready:          readyChecker,
		PaymentStore:   st,
		RazorpayKey:    razorpayKey,
		RazorpaySecret: razorpaySecret,
	}

	adminH := &api.AdminHandler{
		DB:                db,
		EmailerServiceURL: emailerServiceURL,
	}

	// Initialize Kubernetes client for dev panel
	namespace := os.Getenv("KUBERNETES_NAMESPACE")
	if namespace == "" {
		namespace = "studojo"
	}
	k8sClient, err := k8s.NewClient(namespace)
	if err != nil {
		slog.Warn("failed to initialize k8s client, dev panel features will be limited", "error", err)
		k8sClient = nil
	} else {
		slog.Info("k8s client initialized", "namespace", namespace)
	}

	// Initialize GitHub client for CI/CD status
	githubClient := api.NewGitHubClient()

	// Initialize Azure Monitor client
	azureMonitor := api.NewAzureMonitorClient()

	devH := &api.DevHandler{
		DB:           db,
		K8sClient:    k8sClient,
		GitHubClient: githubClient,
		AzureMonitor: azureMonitor,
	}

	// Initialize email handler
	emailerServiceURL := os.Getenv("EMAILER_SERVICE_URL")
	emailH := api.NewEmailHandler(emailerServiceURL)
	if emailerServiceURL != "" {
		slog.Info("email handler initialized", "emailer_service_url", emailerServiceURL)
	} else {
		slog.Info("email handler initialized with default URL", "emailer_service_url", "http://emailer-service:8087")
	}

	// Initialize job outreach handler
	jobOutreachServiceURL := os.Getenv("JOB_OUTREACH_SERVICE_URL")
	jobOutreachH := api.NewJobOutreachHandler(jobOutreachServiceURL)
	if jobOutreachServiceURL != "" {
		slog.Info("job outreach handler initialized", "service_url", jobOutreachServiceURL)
	} else {
		slog.Info("job outreach handler initialized with default URL", "service_url", "http://job-outreach-svc:8000")
	}

	jwks := auth.NewJWKSClient(jwksURL, nil)
	authMW := &auth.Middleware{JWKS: jwks}
	adminMW := &auth.AdminMiddleware{JWKS: jwks, DB: db}
	devMW := &auth.DevMiddleware{JWKS: jwks, DB: db}

	// Initialize rate limiter
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	rateLimiter, err := api.NewRateLimiter(redisURL)
	if err != nil {
		slog.Warn("rate limiter initialization failed, continuing without rate limiting", "error", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.HandleHealth)
	mux.HandleFunc("GET /ready", h.HandleReady)
	mux.Handle("POST /v1/outlines/generate", authMW.Wrap(http.HandlerFunc(h.HandleGenerateOutline)))
	mux.Handle("POST /v1/outlines/edit", authMW.Wrap(http.HandlerFunc(h.HandleEditOutline)))
	mux.Handle("POST /v1/payments/create-order", authMW.Wrap(http.HandlerFunc(h.HandleCreatePaymentOrder)))
	mux.Handle("POST /v1/payments/verify", authMW.Wrap(http.HandlerFunc(h.HandleVerifyPayment)))
	mux.Handle("POST /v1/humanizer/calculate-price", authMW.Wrap(http.HandlerFunc(h.HandleCalculateHumanizerPrice)))
	mux.Handle("POST /v1/humanizer/upload-file", authMW.Wrap(http.HandlerFunc(h.HandleUploadHumanizerFile)))
	mux.Handle("POST /v1/jobs", authMW.Wrap(http.HandlerFunc(h.HandleSubmitJob)))
	mux.Handle("GET /v1/jobs", authMW.Wrap(http.HandlerFunc(h.HandleListJobs)))
	mux.Handle("GET /v1/jobs/{id}", authMW.Wrap(http.HandlerFunc(h.HandleGetJob)))

	// Email routes - public endpoints (no auth)
	mux.Handle("POST /v1/email/forgot-password", http.HandlerFunc(emailH.HandleForgotPassword))
	mux.Handle("POST /v1/email/reset-password", http.HandlerFunc(emailH.HandleResetPassword))

	// Email routes - authenticated endpoints
	mux.Handle("POST /v1/email/change-password", authMW.Wrap(http.HandlerFunc(emailH.HandleChangePassword)))
	mux.Handle("GET /v1/email/preferences/{user_id}", authMW.Wrap(http.HandlerFunc(emailH.HandleGetEmailPreferences)))
	mux.Handle("PUT /v1/email/preferences/{user_id}", authMW.Wrap(http.HandlerFunc(emailH.HandleUpdateEmailPreferences)))

	// Job outreach routes - all authenticated, proxied to job-outreach-svc
	mux.Handle("GET /v1/outreach/health", http.HandlerFunc(jobOutreachH.HandleHealth))
	mux.Handle("POST /v1/outreach/", authMW.Wrap(http.HandlerFunc(jobOutreachH.ProxyAll)))
	mux.Handle("GET /v1/outreach/", authMW.Wrap(http.HandlerFunc(jobOutreachH.ProxyAll)))
	mux.Handle("PUT /v1/outreach/", authMW.Wrap(http.HandlerFunc(jobOutreachH.ProxyAll)))
	mux.Handle("DELETE /v1/outreach/", authMW.Wrap(http.HandlerFunc(jobOutreachH.ProxyAll)))

	// Admin routes
	mux.Handle("GET /v1/admin/users", adminMW.Wrap(http.HandlerFunc(adminH.HandleListUsers)))
	mux.Handle("GET /v1/admin/users/{id}", adminMW.Wrap(http.HandlerFunc(adminH.HandleGetUser)))
	mux.Handle("PATCH /v1/admin/users/{id}", adminMW.Wrap(http.HandlerFunc(adminH.HandleUpdateUser)))
	mux.Handle("GET /v1/admin/dissertations", adminMW.Wrap(http.HandlerFunc(adminH.HandleListDissertations)))
	mux.Handle("GET /v1/admin/careers", adminMW.Wrap(http.HandlerFunc(adminH.HandleListCareers)))
	mux.Handle("GET /v1/admin/jobs/{id}", adminMW.Wrap(http.HandlerFunc(adminH.HandleGetJob)))
	mux.Handle("GET /v1/admin/stats", adminMW.Wrap(http.HandlerFunc(adminH.HandleGetDashboardStats)))
	mux.Handle("GET /v1/admin/emails/scheduled", adminMW.Wrap(http.HandlerFunc(adminH.HandleListScheduledEmails)))
	mux.Handle("DELETE /v1/admin/emails/scheduled/{id}", adminMW.Wrap(http.HandlerFunc(adminH.HandleCancelScheduledEmail)))
	mux.Handle("POST /v1/admin/emails/trigger", adminMW.Wrap(http.HandlerFunc(adminH.HandleTriggerEmail)))

	// Dev panel routes
	mux.Handle("GET /v1/dev/services", devMW.Wrap(http.HandlerFunc(devH.HandleListServices)))
	mux.Handle("GET /v1/dev/services/{service}/history", devMW.Wrap(http.HandlerFunc(devH.HandleGetDeploymentHistory)))
	mux.Handle("POST /v1/dev/services/{service}/rollback", devMW.Wrap(http.HandlerFunc(devH.HandleRollbackDeployment)))
	mux.Handle("POST /v1/dev/services/{service}/scale", devMW.Wrap(http.HandlerFunc(devH.HandleScaleService)))
	mux.Handle("GET /v1/dev/logs", devMW.Wrap(http.HandlerFunc(devH.HandleQueryLogs)))
	mux.Handle("GET /v1/dev/logs/stream", devMW.Wrap(http.HandlerFunc(devH.HandleStreamLogs)))
	mux.Handle("GET /v1/dev/metrics", devMW.Wrap(http.HandlerFunc(devH.HandleQueryMetrics)))
	mux.Handle("GET /v1/dev/ci-cd/status", devMW.Wrap(http.HandlerFunc(devH.HandleGetCICDStatus)))
	mux.Handle("GET /v1/dev/deployments", devMW.Wrap(http.HandlerFunc(devH.HandleGetDeployments)))
	mux.Handle("POST /v1/dev/deployments", devMW.Wrap(http.HandlerFunc(devH.HandleRecordDeployment)))
	mux.Handle("GET /v1/dev/telemetry", devMW.Wrap(http.HandlerFunc(devH.HandleGetTelemetry)))
	mux.Handle("POST /v1/dev/telemetry", devMW.Wrap(http.HandlerFunc(devH.HandleRecordTelemetry)))
	mux.Handle("GET /v1/dev/docs", devMW.Wrap(http.HandlerFunc(devH.HandleListDocs)))
	mux.Handle("GET /v1/dev/docs/{slug}", devMW.Wrap(http.HandlerFunc(devH.HandleGetDoc)))

	// Build middleware stack: SecurityHeaders -> CORS -> RateLimit -> CorrelationID -> Logging -> Routes
	stack := http.Handler(mux)
	stack = api.SecurityHeaders(stack)
	if rateLimiter != nil {
		stack = rateLimiter.RateLimit(stack)
	}
	stack = api.CorrelationID(api.Logging(stack))
	stack = api.CORS(corsOrigins)(stack)

	consumer := messaging.NewRabbitConsumer(msgCfg, wf)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() {
		messaging.RunWithRetry(ctx, consumer, 5*time.Second)
	}()

	// Start progress consumer
	progressConsumer := messaging.NewProgressConsumer(msgCfg, wf)
	go func() {
		messaging.RunProgressConsumerWithRetry(ctx, progressConsumer, 5*time.Second)
	}()

	addr := ":" + port
	srv := &http.Server{Addr: addr, Handler: stack}

	go func() {
		slog.Info("control-plane listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	stop()
	if err := srv.Shutdown(context.Background()); err != nil {
		slog.Error("shutdown failed", "error", err)
		os.Exit(1)
	}
	fmt.Println("control-plane stopped")
}
