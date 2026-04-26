package location

import (
	"time"

	"github.com/google/uuid"
)

// Inbound WS message from rider apps.
type IncomingMessage struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

type LocationUpdate struct {
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Timestamp time.Time `json:"timestamp"`
}

// RiderLocationUpdatedEvent is the Kafka payload for topic `rider.location.updated`.
type RiderLocationUpdatedEvent struct {
	RiderID   uuid.UUID `json:"rider_id"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Timestamp time.Time `json:"timestamp"`
}
