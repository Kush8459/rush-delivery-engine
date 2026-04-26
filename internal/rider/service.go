package rider

import (
	"context"
	"errors"
	"fmt"

	"github.com/Kush8459/rush-delivery-engine/internal/assignment"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrInvalidRequest = errors.New("invalid request")

type Service struct {
	repo *Repository
	rdb  *redis.Client
}

func NewService(repo *Repository, rdb *redis.Client) *Service {
	return &Service{repo: repo, rdb: rdb}
}

func (s *Service) Register(ctx context.Context, req RegisterRequest) (*Rider, error) {
	if req.Name == "" || req.Phone == "" {
		return nil, fmt.Errorf("%w: name and phone required", ErrInvalidRequest)
	}
	return s.repo.Create(ctx, req)
}

// SetStatus toggles a rider's shift state and keeps Redis geo in sync:
//   - AVAILABLE with coords  → GEOADD (so assignment can find them)
//   - OFFLINE or BUSY        → ZREM  (remove from assignable pool)
//
// BUSY is also written by assignment-service, but we idempotently handle it
// here so riders can self-declare unavailable if needed.
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, req SetStatusRequest) (*Rider, error) {
	if !req.Status.Valid() {
		return nil, fmt.Errorf("%w: unknown status %q", ErrInvalidRequest, req.Status)
	}
	if err := s.repo.SetStatus(ctx, id, req.Status); err != nil {
		return nil, err
	}

	switch req.Status {
	case StatusAvailable:
		if req.Lat == nil || req.Lng == nil {
			return nil, fmt.Errorf("%w: lat/lng required when going AVAILABLE", ErrInvalidRequest)
		}
		if err := s.rdb.GeoAdd(ctx, assignment.RidersGeoKey, &redis.GeoLocation{
			Name:      id.String(),
			Latitude:  *req.Lat,
			Longitude: *req.Lng,
		}).Err(); err != nil {
			return nil, fmt.Errorf("geoadd: %w", err)
		}
	case StatusOffline, StatusBusy:
		_ = s.rdb.ZRem(ctx, assignment.RidersGeoKey, id.String()).Err()
	}

	return s.repo.GetByID(ctx, id)
}

type ActiveOrder struct {
	OrderID uuid.UUID `json:"order_id"`
	Status  string    `json:"status"`
}

func (s *Service) GetActiveOrder(ctx context.Context, riderID uuid.UUID) (*ActiveOrder, error) {
	id, status, err := s.repo.ActiveOrder(ctx, riderID)
	if err != nil {
		return nil, err
	}
	return &ActiveOrder{OrderID: id, Status: status}, nil
}
