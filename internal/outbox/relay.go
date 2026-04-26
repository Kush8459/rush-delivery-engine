package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// Publisher is anything that can forward a raw-bytes payload to Kafka.
// Implemented by pkg/kafka.Producer.
type Publisher interface {
	PublishRaw(ctx context.Context, topic, key string, body []byte) error
}

// Relay polls the outbox table and publishes unsent rows to Kafka.
//
// Multiple Relay instances can run concurrently against the same DB — FOR
// UPDATE SKIP LOCKED in the polling query ensures each row is picked up by
// exactly one relay at a time.
type Relay struct {
	db        *pgxpool.Pool
	publisher Publisher
	log       zerolog.Logger
	interval  time.Duration
	batchSize int
}

func NewRelay(db *pgxpool.Pool, publisher Publisher, log zerolog.Logger) *Relay {
	return &Relay{
		db:        db,
		publisher: publisher,
		log:       log.With().Str("component", "outbox-relay").Logger(),
		interval:  500 * time.Millisecond,
		batchSize: 100,
	}
}

// Run blocks until ctx is cancelled. Safe to invoke as a goroutine alongside
// a service's main loop.
func (r *Relay) Run(ctx context.Context) {
	r.log.Info().Dur("interval", r.interval).Int("batch_size", r.batchSize).Msg("relay started")
	defer r.log.Info().Msg("relay stopped")

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.dispatch(ctx); err != nil {
				r.log.Warn().Err(err).Msg("dispatch failed; will retry next tick")
			}
		}
	}
}

// dispatch claims up to batchSize unsent rows, publishes each to Kafka, then
// marks them sent. Any error mid-way rolls back the whole batch — the rows
// remain unsent and the next tick retries them.
func (r *Relay) dispatch(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rows, err := tx.Query(ctx, `
		SELECT id, topic, partition_key, payload, trace_context
		FROM outbox
		WHERE sent_at IS NULL
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, r.batchSize)
	if err != nil {
		return fmt.Errorf("select unsent: %w", err)
	}

	type item struct {
		id       int64
		topic    string
		key      string
		payload  []byte
		traceCtx []byte
	}
	var batch []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.topic, &it.key, &it.payload, &it.traceCtx); err != nil {
			rows.Close()
			return fmt.Errorf("scan: %w", err)
		}
		batch = append(batch, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows err: %w", err)
	}

	if len(batch) == 0 {
		return nil
	}

	ids := make([]int64, 0, len(batch))
	for _, it := range batch {
		// Re-hydrate the trace context captured at Write() time so the
		// producer span becomes a child of the original request's trace.
		publishCtx := ctx
		if len(it.traceCtx) > 0 {
			carrier := propagation.MapCarrier{}
			if err := json.Unmarshal(it.traceCtx, &carrier); err == nil {
				publishCtx = otel.GetTextMapPropagator().Extract(ctx, carrier)
			}
		}
		if err := r.publisher.PublishRaw(publishCtx, it.topic, it.key, it.payload); err != nil {
			return fmt.Errorf("publish id=%d topic=%s: %w", it.id, it.topic, err)
		}
		ids = append(ids, it.id)
	}

	if _, err := tx.Exec(ctx, `UPDATE outbox SET sent_at = NOW() WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("mark sent: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	r.log.Debug().Int("dispatched", len(batch)).Msg("outbox batch dispatched")
	return nil
}
