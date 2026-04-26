package notification

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Kush8459/rush-delivery-engine/internal/assignment"
	"github.com/Kush8459/rush-delivery-engine/internal/eta"
	"github.com/Kush8459/rush-delivery-engine/internal/location"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/segmentio/kafka-go"
)

// Outbound message envelope. The handler-side WS protocol defined in README.md
// uses this shape for everything we push.
type outMsg struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// OrderAssignedHandler: Kafka(order.assigned) → WS broadcast.
func OrderAssignedHandler(hub *Hub, log zerolog.Logger) func(ctx context.Context, msg kafka.Message) error {
	return func(ctx context.Context, msg kafka.Message) error {
		var evt assignment.OrderAssignedEvent
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			log.Error().Err(err).Msg("bad order.assigned")
			return nil
		}
		hub.Broadcast(evt.OrderID, outMsg{
			Type: "order_assigned",
			Payload: map[string]any{
				"order_id":    evt.OrderID,
				"rider_id":    evt.RiderID,
				"assigned_at": evt.AssignedAt,
			},
		})
		return nil
	}
}

// OrderStatusHandler: Kafka(order.status.updated) → WS broadcast.
// Payload is free-form JSON from the producer; we pass it through.
func OrderStatusHandler(hub *Hub, log zerolog.Logger) func(ctx context.Context, msg kafka.Message) error {
	return func(ctx context.Context, msg kafka.Message) error {
		var payload struct {
			OrderID uuid.UUID `json:"order_id"`
			Status  string    `json:"status"`
		}
		if err := json.Unmarshal(msg.Value, &payload); err != nil {
			log.Error().Err(err).Msg("bad order.status.updated")
			return nil
		}
		hub.Broadcast(payload.OrderID, outMsg{
			Type: "order_status_changed",
			Payload: map[string]any{
				"order_id":   payload.OrderID,
				"status":     payload.Status,
				"changed_at": time.Now().UTC(),
			},
		})
		return nil
	}
}

// ETAHandler: Kafka(eta.updated) → WS broadcast.
func ETAHandler(hub *Hub, log zerolog.Logger) func(ctx context.Context, msg kafka.Message) error {
	return func(ctx context.Context, msg kafka.Message) error {
		var evt eta.ETAUpdatedEvent
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			log.Error().Err(err).Msg("bad eta.updated")
			return nil
		}
		hub.Broadcast(evt.OrderID, outMsg{
			Type: "eta_updated",
			Payload: map[string]any{
				"order_id":    evt.OrderID,
				"eta_minutes": evt.ETAMinutes,
				"computed_at": evt.ComputedAt,
			},
		})
		return nil
	}
}

// LocationHandler fans out rider.location.updated, but these events are keyed
// by rider_id — the WS subscribers are keyed by order_id. We need a rider→order
// map to route. locateOrder is the lookup hook (DB query in production).
func LocationHandler(hub *Hub, locateOrder func(ctx context.Context, riderID uuid.UUID) (uuid.UUID, error), log zerolog.Logger) func(ctx context.Context, msg kafka.Message) error {
	return func(ctx context.Context, msg kafka.Message) error {
		var evt location.RiderLocationUpdatedEvent
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			log.Error().Err(err).Msg("bad rider.location.updated")
			return nil
		}
		orderID, err := locateOrder(ctx, evt.RiderID)
		if err != nil {
			// No active order for this rider — silent drop.
			return nil
		}
		hub.Broadcast(orderID, outMsg{
			Type: "rider_location",
			Payload: map[string]any{
				"lat":       evt.Lat,
				"lng":       evt.Lng,
				"timestamp": evt.Timestamp,
			},
		})
		return nil
	}
}
