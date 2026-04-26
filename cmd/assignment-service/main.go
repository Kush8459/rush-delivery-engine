package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Kush8459/rush-delivery-engine/internal/assignment"
	"github.com/Kush8459/rush-delivery-engine/internal/outbox"
	"github.com/Kush8459/rush-delivery-engine/pkg/config"
	"github.com/Kush8459/rush-delivery-engine/pkg/kafka"
	"github.com/Kush8459/rush-delivery-engine/pkg/logger"
	"github.com/Kush8459/rush-delivery-engine/pkg/postgres"
	"github.com/Kush8459/rush-delivery-engine/pkg/redisx"
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
	log := logger.New("assignment-service", cfg.Env)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tpShutdown, err := telemetry.Init(ctx, telemetry.Config{
		Enabled:      cfg.Telemetry.Enabled,
		OTLPEndpoint: cfg.Telemetry.OTLPEndpoint,
		ServiceName:  "assignment-service",
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

	if err := kafka.EnsureTopics(cfg.Kafka.Brokers, kafka.AllTopics...); err != nil {
		return fmt.Errorf("ensure topics: %w", err)
	}

	producer := kafka.NewProducer(cfg.Kafka.Brokers)
	defer producer.Close()

	// Outbox relay: forwards order.assigned / order.status.updated rows written
	// inside the Service's transactions to Kafka.
	relay := outbox.NewRelay(db, producer, log)
	go relay.Run(ctx)

	svc := assignment.NewService(db, rdb, log)

	consumer := kafka.NewConsumer(
		cfg.Kafka.Brokers,
		kafka.TopicOrderPlaced,
		cfg.Kafka.GroupID+".assignment",
		log,
	)
	defer consumer.Close()

	log.Info().Msg("assignment-service starting consumer")
	return consumer.Run(ctx, assignment.Handler(svc, log))
}
