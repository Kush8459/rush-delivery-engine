package eta

import (
	"time"

	"github.com/google/uuid"
)

// ETAUpdatedEvent is the Kafka payload for topic `eta.updated`.
// Consumed by notification-service to push to customer WebSockets.
type ETAUpdatedEvent struct {
	OrderID    uuid.UUID `json:"order_id"`
	RiderID    uuid.UUID `json:"rider_id"`
	ETAMinutes int       `json:"eta_minutes"`
	ComputedAt time.Time `json:"computed_at"`
}
