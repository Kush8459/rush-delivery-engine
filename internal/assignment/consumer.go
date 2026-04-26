package assignment

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Kush8459/rush-delivery-engine/internal/order"
	"github.com/rs/zerolog"
	"github.com/segmentio/kafka-go"
)

// Handler turns an order.placed event into an Assign() call.
// Returning a non-nil error signals "do not commit" — Kafka will redeliver.
// We treat ErrNoRiderAvailable as a committed outcome (no rider won't resolve
// itself on retry; a separate reaper should handle stale unassigned orders).
func Handler(svc *Service, log zerolog.Logger) func(ctx context.Context, msg kafka.Message) error {
	return func(ctx context.Context, msg kafka.Message) error {
		var evt order.OrderPlacedEvent
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			log.Error().Err(err).Msg("bad order.placed payload; skipping")
			return nil // poison-pill: commit & move on rather than loop forever
		}

		err := svc.Assign(ctx, evt.OrderID, evt.PickupLat, evt.PickupLng)
		if errors.Is(err, ErrNoRiderAvailable) {
			log.Warn().Str("order_id", evt.OrderID.String()).Msg("no rider available; dropping")
			return nil
		}
		return err
	}
}
