package kafka

import (
	"context"
	"encoding/json"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Producer is a thin wrapper around kafka-go's Writer that speaks JSON.
// One Producer can write to multiple topics (topic set per message).
type Producer struct {
	w *kafka.Writer
}

func NewProducer(brokers []string) *Producer {
	return &Producer{
		w: &kafka.Writer{
			Addr:                   kafka.TCP(brokers...),
			Balancer:               &kafka.Hash{}, // hash(key) → partition, guarantees per-key ordering
			RequiredAcks:           kafka.RequireAll,
			AllowAutoTopicCreation: true,
			BatchTimeout:           20 * time.Millisecond,
		},
	}
}

// Publish marshals payload as JSON and writes to topic. key controls partition
// assignment — use order_id for order-scoped events so ordering is preserved.
func (p *Producer) Publish(ctx context.Context, topic, key string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.PublishRaw(ctx, topic, key, body)
}

// PublishRaw writes pre-marshalled bytes. Used by the outbox relay where the
// payload is already stored as JSONB and we just want to forward it.
// The current OTel trace context is injected into Kafka message headers so
// the consumer can continue the same trace.
func (p *Producer) PublishRaw(ctx context.Context, topic, key string, body []byte) error {
	tracer := otel.Tracer("pkg/kafka")
	ctx, span := tracer.Start(ctx, topic+" publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			semconv.MessagingSystemKey.String("kafka"),
			semconv.MessagingDestinationName(topic),
			attribute.String("messaging.kafka.message.key", key),
		),
	)
	defer span.End()

	msg := kafka.Message{
		Topic:   topic,
		Key:     []byte(key),
		Value:   body,
		Time:    time.Now(),
		Headers: []kafka.Header{},
	}
	otel.GetTextMapPropagator().Inject(ctx, headerCarrier{headers: &msg.Headers})

	if err := p.w.WriteMessages(ctx, msg); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

func (p *Producer) Close() error { return p.w.Close() }
