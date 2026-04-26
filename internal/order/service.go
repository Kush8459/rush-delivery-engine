package order

import (
	"context"
	"errors"
	"fmt"

	"github.com/Kush8459/rush-delivery-engine/pkg/metrics"
	"github.com/google/uuid"
)

var (
	ErrInvalidRequest = errors.New("invalid request")
)

// Service orchestrates order lifecycle writes. Kafka publishing is deferred to
// the outbox relay — the service only touches the DB.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// Place validates the request and creates the order. The repository writes the
// order row, audit event, and outbox row in a single transaction, so `order.placed`
// reaches Kafka exactly when the order reaches Postgres — never out of sync.
func (s *Service) Place(ctx context.Context, req PlaceOrderRequest) (*Order, error) {
	if err := validatePlace(req); err != nil {
		return nil, err
	}
	o, err := s.repo.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	metrics.IncEvent("order.placed")
	return o, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Order, error) {
	return s.repo.GetByID(ctx, id)
}

// Cancel transitions an order to CANCELLED via the state machine.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID) (*Order, error) {
	return s.Transition(ctx, id, StatusCancelled)
}

// Transition moves the order to `to`, validated by the state machine. The
// repository's UpdateStatus writes the status change, audit event, and outbox
// row atomically.
func (s *Service) Transition(ctx context.Context, id uuid.UUID, to Status) (*Order, error) {
	o, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := ValidateTransition(o.Status, to); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateStatus(ctx, id, to); err != nil {
		return nil, err
	}
	metrics.IncEvent("order.status." + string(to))
	o.Status = to
	return o, nil
}

func validatePlace(r PlaceOrderRequest) error {
	if r.CustomerID == uuid.Nil {
		return fmt.Errorf("%w: customer_id required", ErrInvalidRequest)
	}
	if r.RestaurantID == uuid.Nil {
		return fmt.Errorf("%w: restaurant_id required", ErrInvalidRequest)
	}
	if r.TotalAmount <= 0 {
		return fmt.Errorf("%w: total_amount must be > 0", ErrInvalidRequest)
	}
	if r.DeliveryAddress == "" {
		return fmt.Errorf("%w: delivery_address required", ErrInvalidRequest)
	}
	if !validLatLng(r.PickupLat, r.PickupLng) || !validLatLng(r.DropoffLat, r.DropoffLng) {
		return fmt.Errorf("%w: invalid coordinates", ErrInvalidRequest)
	}
	return nil
}

func validLatLng(lat, lng float64) bool {
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}
