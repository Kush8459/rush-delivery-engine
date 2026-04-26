package kafka

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Handler processes a single message. Return an error to NACK (message will be
// redelivered on the next consumer cycle); returning nil commits the offset.
type Handler func(ctx context.Context, msg kafka.Message) error

// Consumer wraps kafka-go's Reader with a simple Run loop.
// GroupID is required — it's what enables at-least-once delivery + rebalancing.
type Consumer struct {
	r   *kafka.Reader
	log zerolog.Logger
}

func NewConsumer(brokers []string, topic, groupID string, log zerolog.Logger) *Consumer {
	return &Consumer{
		r: kafka.NewReader(kafka.ReaderConfig{
			Brokers:        brokers,
			GroupID:        groupID,
			Topic:          topic,
			MinBytes:       1,
			MaxBytes:       10 << 20, // 10MB
			CommitInterval: 0,        // commit manually after successful handle
			MaxWait:        500 * time.Millisecond,
		}),
		log: log.With().Str("topic", topic).Str("group", groupID).Logger(),
	}
}

// Run blocks until ctx is cancelled. On handler error we log and do NOT commit,
// so Kafka will redeliver the message on the next fetch.
func (c *Consumer) Run(ctx context.Context, h Handler) error {
	c.log.Info().Msg("consumer started")
	defer c.log.Info().Msg("consumer stopped")

	for {
		msg, err := c.r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			c.log.Error().Err(err).Msg("fetch failed")
			continue
		}

		// Extract any upstream trace context from the message headers and start
		// a child consumer span. Handlers inherit this context so spans they
		// start are children of the consume span.
		msgCtx := otel.GetTextMapPropagator().Extract(ctx, headerCarrier{headers: &msg.Headers})
		tracer := otel.Tracer("pkg/kafka")
		msgCtx, span := tracer.Start(msgCtx, msg.Topic+" process",
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				semconv.MessagingSystemKey.String("kafka"),
				semconv.MessagingDestinationName(msg.Topic),
				attribute.String("messaging.kafka.message.key", string(msg.Key)),
			),
		)

		if err := h(msgCtx, msg); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			span.End()
			c.log.Error().Err(err).Str("key", string(msg.Key)).Msg("handler failed; will redeliver")
			continue
		}
		span.End()

		if err := c.r.CommitMessages(ctx, msg); err != nil {
			c.log.Error().Err(err).Msg("commit failed")
		}
	}
}

func (c *Consumer) Close() error { return c.r.Close() }
