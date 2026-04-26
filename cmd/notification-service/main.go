package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Kush8459/rush-delivery-engine/internal/notification"
	"github.com/Kush8459/rush-delivery-engine/pkg/config"
	"github.com/Kush8459/rush-delivery-engine/pkg/kafka"
	"github.com/Kush8459/rush-delivery-engine/pkg/logger"
	"github.com/Kush8459/rush-delivery-engine/pkg/metrics"
	"github.com/Kush8459/rush-delivery-engine/pkg/postgres"
	"github.com/Kush8459/rush-delivery-engine/pkg/telemetry"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	log := logger.New("notification-service", cfg.Env)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tpShutdown, err := telemetry.Init(ctx, telemetry.Config{
		Enabled:      cfg.Telemetry.Enabled,
		OTLPEndpoint: cfg.Telemetry.OTLPEndpoint,
		ServiceName:  "notification-service",
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

	if err := kafka.EnsureTopics(cfg.Kafka.Brokers, kafka.AllTopics...); err != nil {
		return fmt.Errorf("ensure topics: %w", err)
	}

	hub := notification.NewHub(log)
	handler := notification.NewHandler(hub, log)

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(metrics.HTTPMiddleware("notification-service", func(req *http.Request) string {
		return chi.RouteContext(req.Context()).RoutePattern()
	}))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Handle("/metrics", metrics.Handler())
	handler.Routes(r)

	addr := fmt.Sprintf(":%d", cfg.Services.NotifyPort)
	srv := &http.Server{Addr: addr, Handler: otelhttp.NewHandler(r, "notification-service"), ReadHeaderTimeout: 5 * time.Second}

	// Consumers: each topic runs on its own group-suffixed consumer so offsets
	// are independent. Group ID collisions between services would cause missed
	// events — prefix protects against that.
	consumers := []*kafka.Consumer{
		kafka.NewConsumer(cfg.Kafka.Brokers, kafka.TopicOrderAssigned, cfg.Kafka.GroupID+".notify.assigned", log),
		kafka.NewConsumer(cfg.Kafka.Brokers, kafka.TopicOrderStatusUpdated, cfg.Kafka.GroupID+".notify.status", log),
		kafka.NewConsumer(cfg.Kafka.Brokers, kafka.TopicETAUpdated, cfg.Kafka.GroupID+".notify.eta", log),
		kafka.NewConsumer(cfg.Kafka.Brokers, kafka.TopicRiderLocationUpdate, cfg.Kafka.GroupID+".notify.loc", log),
	}
	defer func() {
		for _, c := range consumers {
			_ = c.Close()
		}
	}()

	locateOrder := newRiderOrderLookup(db)

	handlers := []kafka.Handler{
		notification.OrderAssignedHandler(hub, log),
		notification.OrderStatusHandler(hub, log),
		notification.ETAHandler(hub, log),
		notification.LocationHandler(hub, locateOrder, log),
	}

	var wg sync.WaitGroup
	for i, c := range consumers {
		wg.Add(1)
		go func(c *kafka.Consumer, h kafka.Handler) {
			defer wg.Done()
			if err := c.Run(ctx, h); err != nil {
				log.Error().Err(err).Msg("consumer exited")
			}
		}(c, handlers[i])
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info().Str("addr", addr).Msg("notification-service listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info().Msg("shutdown signal received")
	case err := <-serverErr:
		cancel()
		return err
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	wg.Wait()
	return nil
}

// newRiderOrderLookup returns a closure that finds the current non-terminal
// order_id for a given rider. It's called on every rider.location.updated
// event, so we keep the query tight (indexed on rider_id).
func newRiderOrderLookup(db *pgxpool.Pool) func(ctx context.Context, riderID uuid.UUID) (uuid.UUID, error) {
	return func(ctx context.Context, riderID uuid.UUID) (uuid.UUID, error) {
		var id uuid.UUID
		err := db.QueryRow(ctx, `
			SELECT id FROM orders
			WHERE rider_id = $1 AND status NOT IN ('DELIVERED','CANCELLED')
			ORDER BY created_at DESC LIMIT 1
		`, riderID).Scan(&id)
		return id, err
	}
}
