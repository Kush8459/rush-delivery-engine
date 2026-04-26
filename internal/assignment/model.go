package assignment

import (
	"time"

	"github.com/google/uuid"
)

// OrderAssignedEvent is the Kafka payload for topic `order.assigned`.
// Emitted after a rider has been atomically selected for an order.
type OrderAssignedEvent struct {
	OrderID    uuid.UUID `json:"order_id"`
	RiderID    uuid.UUID `json:"rider_id"`
	AssignedAt time.Time `json:"assigned_at"`
}
