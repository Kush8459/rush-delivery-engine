package eta

import (
	"context"
	"errors"
	"time"

	"github.com/Kush8459/rush-delivery-engine/internal/location"
	"github.com/Kush8459/rush-delivery-engine/pkg/kafka"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// activeOrder captures what we need to compute an ETA for a rider's current job.
type activeOrder struct {
	OrderID    uuid.UUID
	Status     string
	PickupLat  float64
	PickupLng  float64
	DropoffLat float64
	DropoffLng float64
}

type Service struct {
	db       *pgxpool.Pool
	producer *kafka.Producer
	log      zerolog.Logger
}

func NewService(db *pgxpool.Pool, producer *kafka.Producer, log zerolog.Logger) *Service {
	return &Service{db: db, producer: producer, log: log}
}

// OnLocation consumes a rider.location.updated event, looks up the rider's
// active order, computes an ETA (to pickup if not picked up yet, else dropoff),
// persists it, and publishes eta.updated.
//
// Riders with no active order are a no-op — common case when they just came online.
func (s *Service) OnLocation(ctx context.Context, evt location.RiderLocationUpdatedEvent) error {
	ao, err := s.activeOrderForRider(ctx, evt.RiderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	var targetLat, targetLng float64
	switch ao.Status {
	case "PICKED_UP":
		targetLat, targetLng = ao.DropoffLat, ao.DropoffLng
	default:
		targetLat, targetLng = ao.PickupLat, ao.PickupLng
	}

	mins := Compute(evt.Lat, evt.Lng, targetLat, targetLng)

	if _, err := s.db.Exec(ctx,
		`UPDATE orders SET eta_minutes = $1, updated_at = NOW() WHERE id = $2`,
		mins, ao.OrderID,
	); err != nil {
		return err
	}

	out := ETAUpdatedEvent{
		OrderID:    ao.OrderID,
		RiderID:    evt.RiderID,
		ETAMinutes: mins,
		ComputedAt: time.Now().UTC(),
	}
	return s.producer.Publish(ctx, kafka.TopicETAUpdated, ao.OrderID.String(), out)
}

func (s *Service) activeOrderForRider(ctx context.Context, riderID uuid.UUID) (*activeOrder, error) {
	var ao activeOrder
	// Match any non-terminal order. We don't gate on ACCEPTED because the
	// demo's assignment flow leaves orders in PLACED — a production flow
	// would advance the status on restaurant acceptance.
	err := s.db.QueryRow(ctx, `
		SELECT id, status, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng
		FROM orders
		WHERE rider_id = $1 AND status NOT IN ('DELIVERED','CANCELLED')
		ORDER BY created_at DESC LIMIT 1
	`, riderID).Scan(&ao.OrderID, &ao.Status, &ao.PickupLat, &ao.PickupLng, &ao.DropoffLat, &ao.DropoffLng)
	if err != nil {
		return nil, err
	}
	return &ao, nil
}
