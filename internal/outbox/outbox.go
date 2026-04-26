// Package outbox implements the transactional outbox pattern.
//
// Writers insert an outbox row in the same DB transaction as their business
// write. A Relay goroutine then polls unsent rows, publishes them to Kafka,
// and marks them sent. If Kafka is unavailable the events queue in the
// outbox table and drain on recovery — no events are lost to a mid-flight
// Kafka outage.
//
// At-least-once semantics: a crash between "published to Kafka" and "UPDATE
// sent_at" will cause the row to be republished next tick. Consumers must be
// idempotent. In this codebase that's already true: the rider-claim UPDATE
// is status-guarded, the notification hub just fans out, and the ETA and
// status handlers are deterministic given the event payload.
package outbox

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// Write inserts an outbox row inside the caller's transaction. The payload is
// JSON-marshalled; the relay will forward it verbatim to Kafka.
//
// The current OTel trace context is also captured and stored on the row so
// the relay can re-inject it when publishing — keeping the HTTP → DB → Kafka
// → consumer trace unbroken across the async hop.
//
// partitionKey controls Kafka partition assignment — pass the aggregate ID as
// a string (e.g. order_id) for per-aggregate ordering.
func Write(ctx context.Context, tx pgx.Tx, aggregateID uuid.UUID, topic, partitionKey string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// Capture trace context as a flat map (propagation.MapCarrier marshals
	// cleanly to JSON).
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	traceCtx, err := json.Marshal(carrier)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO outbox (aggregate_id, topic, partition_key, payload, trace_context)
		VALUES ($1, $2, $3, $4, $5)
	`, aggregateID, topic, partitionKey, body, traceCtx)
	return err
}
