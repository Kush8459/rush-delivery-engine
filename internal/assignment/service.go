package assignment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Kush8459/rush-delivery-engine/internal/outbox"
	"github.com/Kush8459/rush-delivery-engine/pkg/kafka"
	"github.com/Kush8459/rush-delivery-engine/pkg/metrics"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// noRiderEvent matches the shape notification-service expects for
// order.status.updated so subscribed clients receive CANCELLED + reason.
type noRiderEvent struct {
	OrderID   uuid.UUID `json:"order_id"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason"`
	ChangedAt time.Time `json:"changed_at"`
}

// RidersGeoKey is the Redis key holding rider GPS positions (GEOADD members).
// Members are rider UUIDs as strings; coordinates are their latest position.
const RidersGeoKey = "riders:geo"

// searchRadiusKm is how far out we look for an available rider. Widened to
// 25 km so a demo pickup anywhere in Delhi finds the single seeded rider.
// Production systems tier this: try 2 km → 5 km → 10 km with increasing ETAs.
const searchRadiusKm = 25.0

var ErrNoRiderAvailable = errors.New("no available rider within radius")

type Service struct {
	db  *pgxpool.Pool
	rdb *redis.Client
	log zerolog.Logger
}

// NewService constructs the assignment service. Kafka publishing is deferred
// to the outbox relay — this service only touches Postgres and Redis.
func NewService(db *pgxpool.Pool, rdb *redis.Client, log zerolog.Logger) *Service {
	return &Service{db: db, rdb: rdb, log: log}
}

// Assign finds the nearest AVAILABLE rider to (lat, lng), flips them to BUSY,
// attaches them to the order, and emits order.assigned.
//
// The race we care about: two orders arriving simultaneously for the same
// nearest rider. Guarded by an UPDATE ... WHERE status = 'AVAILABLE' — if two
// callers race, only one row update succeeds; the other falls through to try
// the next candidate.
func (s *Service) Assign(ctx context.Context, orderID uuid.UUID, pickupLat, pickupLng float64) error {
	candidates, err := s.nearestAvailableRiders(ctx, pickupLat, pickupLng, 10)
	if err != nil {
		return fmt.Errorf("redis nearest: %w", err)
	}

	// Empty candidates is expected (e.g. no rider online in radius). Fall through
	// to the "no rider" path below rather than short-circuiting, so the order is
	// always resolved (CANCELLED + notification) instead of hanging in PLACED.
	for _, riderID := range candidates {
		claimed, err := s.claimRider(ctx, orderID, riderID)
		if err != nil {
			s.log.Warn().Err(err).Str("rider_id", riderID.String()).Msg("claim failed; trying next")
			continue
		}
		if !claimed {
			continue
		}

		// Remove claimed rider from geo index so they're not picked for the next order.
		if err := s.rdb.ZRem(ctx, RidersGeoKey, riderID.String()).Err(); err != nil {
			s.log.Warn().Err(err).Msg("failed to remove rider from geo index")
		}

		s.log.Info().Str("order_id", orderID.String()).Str("rider_id", riderID.String()).Msg("order assigned")
		metrics.IncEvent("order.assigned")
		return nil
	}

	// No candidate or all claims lost — flip the order to CANCELLED and enqueue
	// an outbox notification in a single transaction so both happen or neither.
	if err := s.markCancelled(ctx, orderID, "no rider available"); err != nil {
		s.log.Error().Err(err).Str("order_id", orderID.String()).Msg("failed to mark cancelled")
		return err
	}
	metrics.IncEvent("order.assignment_failed")
	return ErrNoRiderAvailable
}

// markCancelled flips a still-PLACED order to CANCELLED and writes an outbox
// row for order.status.updated. Tx-wrapped so the row flip and the event
// either both happen or neither.
func (s *Service) markCancelled(ctx context.Context, orderID uuid.UUID, reason string) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `
		UPDATE orders SET status='CANCELLED', updated_at=NOW()
		WHERE id=$1 AND status='PLACED'
	`, orderID)
	if err != nil {
		return fmt.Errorf("update order cancelled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Already moved by another actor — nothing to do.
		return tx.Commit(ctx)
	}

	evt := noRiderEvent{
		OrderID:   orderID,
		Status:    "CANCELLED",
		Reason:    reason,
		ChangedAt: time.Now().UTC(),
	}
	if err := outbox.Write(ctx, tx, orderID, kafka.TopicOrderStatusUpdated, orderID.String(), evt); err != nil {
		return fmt.Errorf("write outbox: %w", err)
	}
	return tx.Commit(ctx)
}

// nearestAvailableRiders returns up to `limit` rider UUIDs sorted by proximity.
// Uses GEOSEARCH (GEORADIUS is deprecated as of Redis 6.2).
func (s *Service) nearestAvailableRiders(ctx context.Context, lat, lng float64, limit int) ([]uuid.UUID, error) {
	res, err := s.rdb.GeoSearch(ctx, RidersGeoKey, &redis.GeoSearchQuery{
		Longitude:  lng,
		Latitude:   lat,
		Radius:     searchRadiusKm,
		RadiusUnit: "km",
		Sort:       "ASC",
		Count:      limit,
	}).Result()
	if err != nil {
		return nil, err
	}

	out := make([]uuid.UUID, 0, len(res))
	for _, name := range res {
		id, err := uuid.Parse(name)
		if err != nil {
			s.log.Warn().Str("member", name).Msg("skipping non-uuid geo member")
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// claimRider atomically transitions a rider AVAILABLE→BUSY and attaches them
// to the order. Returns (true, nil) if we won the claim, (false, nil) if the
// rider was taken or no longer AVAILABLE.
func (s *Service) claimRider(ctx context.Context, orderID, riderID uuid.UUID) (bool, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `
		UPDATE riders SET status = 'BUSY', updated_at = NOW()
		WHERE id = $1 AND status = 'AVAILABLE'
	`, riderID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE orders SET rider_id = $1, updated_at = NOW() WHERE id = $2
	`, riderID, orderID); err != nil {
		return false, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events (order_id, event_type, payload)
		VALUES ($1, 'order.assigned', jsonb_build_object('rider_id', $2::text))
	`, orderID, riderID.String()); err != nil {
		return false, err
	}

	evt := OrderAssignedEvent{
		OrderID:    orderID,
		RiderID:    riderID,
		AssignedAt: time.Now().UTC(),
	}
	if err := outbox.Write(ctx, tx, orderID, kafka.TopicOrderAssigned, orderID.String(), evt); err != nil {
		return false, fmt.Errorf("write outbox: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
