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
		corsOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000"}
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
		Workflow:      wf,
		Ready:         readyChecker,
		PaymentStore:  st,
		RazorpayKey:   razorpayKey,
		RazorpaySecret: razorpaySecret,
	}

	jwks := auth.NewJWKSClient(jwksURL, nil)
	authMW := &auth.Middleware{JWKS: jwks}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.HandleHealth)
	mux.HandleFunc("GET /ready", h.HandleReady)
	mux.Handle("POST /v1/outlines/generate", authMW.Wrap(http.HandlerFunc(h.HandleGenerateOutline)))
	mux.Handle("POST /v1/outlines/edit", authMW.Wrap(http.HandlerFunc(h.HandleEditOutline)))
	mux.Handle("POST /v1/payments/create-order", authMW.Wrap(http.HandlerFunc(h.HandleCreatePaymentOrder)))
	mux.Handle("POST /v1/payments/verify", authMW.Wrap(http.HandlerFunc(h.HandleVerifyPayment)))
	mux.Handle("POST /v1/jobs", authMW.Wrap(http.HandlerFunc(h.HandleSubmitJob)))
	mux.Handle("GET /v1/jobs", authMW.Wrap(http.HandlerFunc(h.HandleListJobs)))
	mux.Handle("GET /v1/jobs/{id}", authMW.Wrap(http.HandlerFunc(h.HandleGetJob)))

	stack := api.CORS(corsOrigins)(api.CorrelationID(api.Logging(mux)))

	consumer := messaging.NewRabbitConsumer(msgCfg, wf)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() {
		messaging.RunWithRetry(ctx, consumer, 5*time.Second)
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
