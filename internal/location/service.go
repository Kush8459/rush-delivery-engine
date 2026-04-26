package location

import (
	"context"
	"time"

	"github.com/Kush8459/rush-delivery-engine/internal/assignment"
	"github.com/Kush8459/rush-delivery-engine/pkg/kafka"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Service handles a single location update: write to Redis geo, publish Kafka.
// Redis is the source of truth for *current* position; Kafka is the event stream
// that triggers ETA recomputation and customer push.
type Service struct {
	rdb      *redis.Client
	producer *kafka.Producer
}

func NewService(rdb *redis.Client, producer *kafka.Producer) *Service {
	return &Service{rdb: rdb, producer: producer}
}

func (s *Service) UpdateLocation(ctx context.Context, riderID uuid.UUID, lat, lng float64, ts time.Time) error {
	if err := s.rdb.GeoAdd(ctx, assignment.RidersGeoKey, &redis.GeoLocation{
		Name:      riderID.String(),
		Latitude:  lat,
		Longitude: lng,
	}).Err(); err != nil {
		return err
	}

	evt := RiderLocationUpdatedEvent{
		RiderID:   riderID,
		Lat:       lat,
		Lng:       lng,
		Timestamp: ts,
	}
	// Partition by rider_id so ETA service sees their updates in order.
	return s.producer.Publish(ctx, kafka.TopicRiderLocationUpdate, riderID.String(), evt)
}
