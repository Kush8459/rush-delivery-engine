package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Kush8459/rush-delivery-engine/internal/eta"
	"github.com/Kush8459/rush-delivery-engine/pkg/config"
	"github.com/Kush8459/rush-delivery-engine/pkg/kafka"
	"github.com/Kush8459/rush-delivery-engine/pkg/logger"
	"github.com/Kush8459/rush-delivery-engine/pkg/postgres"
	"github.com/Kush8459/rush-delivery-engine/pkg/telemetry"
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
	log := logger.New("eta-service", cfg.Env)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tpShutdown, err := telemetry.Init(ctx, telemetry.Config{
		Enabled:      cfg.Telemetry.Enabled,
		OTLPEndpoint: cfg.Telemetry.OTLPEndpoint,
		ServiceName:  "eta-service",
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

	producer := kafka.NewProducer(cfg.Kafka.Brokers)
	defer producer.Close()

	svc := eta.NewService(db, producer, log)

	consumer := kafka.NewConsumer(
		cfg.Kafka.Brokers,
		kafka.TopicRiderLocationUpdate,
		cfg.Kafka.GroupID+".eta",
		log,
	)
	defer consumer.Close()

	log.Info().Msg("eta-service starting consumer")
	return consumer.Run(ctx, eta.Handler(svc, log))
}
