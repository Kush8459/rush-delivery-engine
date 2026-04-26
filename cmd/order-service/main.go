package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Kush8459/rush-delivery-engine/internal/order"
	"github.com/Kush8459/rush-delivery-engine/internal/outbox"
	"github.com/Kush8459/rush-delivery-engine/pkg/auth"
	"github.com/Kush8459/rush-delivery-engine/pkg/config"
	"github.com/Kush8459/rush-delivery-engine/pkg/kafka"
	"github.com/Kush8459/rush-delivery-engine/pkg/logger"
	"github.com/Kush8459/rush-delivery-engine/pkg/metrics"
	demw "github.com/Kush8459/rush-delivery-engine/pkg/middleware"
	"github.com/Kush8459/rush-delivery-engine/pkg/postgres"
	"github.com/Kush8459/rush-delivery-engine/pkg/redisx"
	"github.com/Kush8459/rush-delivery-engine/pkg/telemetry"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config/config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logger.New("order-service", cfg.Env)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tpShutdown, err := telemetry.Init(ctx, telemetry.Config{
		Enabled:      cfg.Telemetry.Enabled,
		OTLPEndpoint: cfg.Telemetry.OTLPEndpoint,
		ServiceName:  "order-service",
	})
	if err != nil {
		return fmt.Errorf("init telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tpShutdown(shutdownCtx)
	}()

	db, err := postgres.New(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer db.Close()

	rdb, err := redisx.New(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer rdb.Close()

	if err := kafka.EnsureTopics(cfg.Kafka.Brokers, kafka.AllTopics...); err != nil {
		return fmt.Errorf("ensure topics: %w", err)
	}

	producer := kafka.NewProducer(cfg.Kafka.Brokers)
	defer producer.Close()

	// Outbox relay: polls the outbox table and forwards rows to Kafka.
	// Running it in-process with the producer means the relay and the
	// business write share a DB connection pool and a graceful-shutdown path.
	relay := outbox.NewRelay(db, producer, log)
	go relay.Run(ctx)

	repo := order.NewRepository(db)
	svc := order.NewService(repo)
	handler := order.NewHandler(svc, log)

	signer := auth.NewSigner(cfg.Auth.JWTSecret, cfg.Auth.TTLHours)
	authHandler := order.NewAuthHandler(signer)

	limiter := demw.NewSlidingWindow(rdb, cfg.RateLimit.OrdersPerMinute, time.Minute, "rl:orders")

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(15 * time.Second))
	r.Use(metrics.HTTPMiddleware("order-service", func(req *http.Request) string {
		return chi.RouteContext(req.Context()).RoutePattern()
	}))
	// Throttle order placement per authenticated customer.
	// Falls back to the header if no JWT is present (e.g. /login pre-auth),
	// so the limiter never crashes if an unauthenticated path slips in.
	r.Use(limiter.HTTP(log, func(req *http.Request) string {
		if req.Method != http.MethodPost || req.URL.Path != "/api/v1/orders" {
			return ""
		}
		if c := auth.FromContext(req.Context()); c != nil {
			return c.CustomerID.String()
		}
		return req.Header.Get("X-Customer-Id")
	}))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Handle("/metrics", metrics.Handler())

	// Public endpoints.
	r.Post("/api/v1/auth/login", authHandler.Login)

	// Protected endpoints: JWT required. Wrap with auth middleware in a sub-router
	// so /healthz and /metrics stay open for ops tooling.
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(signer))
		handler.Routes(r)
	})

	addr := fmt.Sprintf(":%d", cfg.Services.OrderPort)
	srv := &http.Server{
		Addr:              addr,
		Handler:           otelhttp.NewHandler(r, "order-service"),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Run server and wait for shutdown signal concurrently.
	serverErr := make(chan error, 1)
	go func() {
		log.Info().Str("addr", addr).Msg("order-service listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info().Msg("shutdown signal received")
	case err := <-serverErr:
		return err
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return srv.Shutdown(shutdownCtx)
}
