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

	"github.com/Kush8459/rush-delivery-engine/internal/rider"
	"github.com/Kush8459/rush-delivery-engine/pkg/config"
	"github.com/Kush8459/rush-delivery-engine/pkg/logger"
	"github.com/Kush8459/rush-delivery-engine/pkg/metrics"
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
	log := logger.New("rider-service", cfg.Env)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tpShutdown, err := telemetry.Init(ctx, telemetry.Config{
		Enabled:      cfg.Telemetry.Enabled,
		OTLPEndpoint: cfg.Telemetry.OTLPEndpoint,
		ServiceName:  "rider-service",
	})
	if err != nil {
		return fmt.Errorf("init telemetry: %w", err)
	}
	defer func() {
		sctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = tpShutdown(sctx)
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

	repo := rider.NewRepository(db)
	svc := rider.NewService(repo, rdb)
	handler := rider.NewHandler(svc, log)

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer, middleware.Timeout(15*time.Second))
	r.Use(metrics.HTTPMiddleware("rider-service", func(req *http.Request) string {
		return chi.RouteContext(req.Context()).RoutePattern()
	}))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Handle("/metrics", metrics.Handler())
	handler.Routes(r)

	addr := fmt.Sprintf(":%d", cfg.Services.RiderPort)
	srv := &http.Server{Addr: addr, Handler: otelhttp.NewHandler(r, "rider-service"), ReadHeaderTimeout: 5 * time.Second}

	serverErr := make(chan error, 1)
	go func() {
		log.Info().Str("addr", addr).Msg("rider-service listening")
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
