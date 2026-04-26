package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Kush8459/rush-delivery-engine/internal/outbox"
	"github.com/Kush8459/rush-delivery-engine/pkg/kafka"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("order not found")

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// Create inserts an order, its first audit event (PLACED), and the matching
// outbox row — all in one transaction. The relay picks up the outbox row
// asynchronously and publishes `order.placed` to Kafka, guaranteeing that the
// order row and its event either both exist or both don't.
func (r *Repository) Create(ctx context.Context, req PlaceOrderRequest) (*Order, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if Commit succeeded

	o := &Order{
		CustomerID:      req.CustomerID,
		RestaurantID:    req.RestaurantID,
		Status:          StatusPlaced,
		TotalAmount:     req.TotalAmount,
		DeliveryAddress: req.DeliveryAddress,
		PickupLat:       req.PickupLat,
		PickupLng:       req.PickupLng,
		DropoffLat:      req.DropoffLat,
		DropoffLng:      req.DropoffLng,
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO orders (
			customer_id, restaurant_id, status, total_amount, delivery_address,
			pickup_lat, pickup_lng, dropoff_lat, dropoff_lng
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, created_at, updated_at
	`,
		o.CustomerID, o.RestaurantID, o.Status, o.TotalAmount, o.DeliveryAddress,
		o.PickupLat, o.PickupLng, o.DropoffLat, o.DropoffLng,
	).Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert order: %w", err)
	}

	eventPayload, _ := json.Marshal(map[string]any{
		"status":        o.Status,
		"total_amount":  o.TotalAmount,
		"restaurant_id": o.RestaurantID,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events (order_id, event_type, payload)
		VALUES ($1, 'order.placed', $2)
	`, o.ID, eventPayload); err != nil {
		return nil, fmt.Errorf("insert event: %w", err)
	}

	outboxEvt := OrderPlacedEvent{
		OrderID:      o.ID,
		CustomerID:   o.CustomerID,
		RestaurantID: o.RestaurantID,
		PickupLat:    o.PickupLat,
		PickupLng:    o.PickupLng,
		DropoffLat:   o.DropoffLat,
		DropoffLng:   o.DropoffLng,
		PlacedAt:     time.Now().UTC(),
	}
	if err := outbox.Write(ctx, tx, o.ID, kafka.TopicOrderPlaced, o.ID.String(), outboxEvt); err != nil {
		return nil, fmt.Errorf("write outbox: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return o, nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*Order, error) {
	var o Order
	err := r.db.QueryRow(ctx, `
		SELECT id, customer_id, rider_id, restaurant_id, status, total_amount,
		       delivery_address, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
		       eta_minutes, created_at, updated_at
		FROM orders WHERE id = $1
	`, id).Scan(
		&o.ID, &o.CustomerID, &o.RiderID, &o.RestaurantID, &o.Status, &o.TotalAmount,
		&o.DeliveryAddress, &o.PickupLat, &o.PickupLng, &o.DropoffLat, &o.DropoffLng,
		&o.ETAMinutes, &o.CreatedAt, &o.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// UpdateStatus transitions the order, writes an audit event, and enqueues an
// order.status.updated outbox row — all in one transaction.
// Caller must have validated the transition via ValidateTransition.
func (r *Repository) UpdateStatus(ctx context.Context, id uuid.UUID, to Status) error {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `
		UPDATE orders SET status = $1, updated_at = NOW() WHERE id = $2
	`, to, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	eventPayload, _ := json.Marshal(map[string]any{"status": to})
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_events (order_id, event_type, payload)
		VALUES ($1, $2, $3)
	`, id, "order.status."+string(to), eventPayload); err != nil {
		return err
	}

	outboxEvt := map[string]any{
		"order_id":   id,
		"status":     to,
		"changed_at": time.Now().UTC(),
	}
	if err := outbox.Write(ctx, tx, id, kafka.TopicOrderStatusUpdated, id.String(), outboxEvt); err != nil {
		return fmt.Errorf("write outbox: %w", err)
	}

	return tx.Commit(ctx)
}
