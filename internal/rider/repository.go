package rider

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("rider not found")

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

func (r *Repository) Create(ctx context.Context, req RegisterRequest) (*Rider, error) {
	rd := &Rider{
		Name:   req.Name,
		Phone:  req.Phone,
		Status: StatusOffline,
	}
	err := r.db.QueryRow(ctx, `
		INSERT INTO riders (name, phone, status)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at
	`, rd.Name, rd.Phone, rd.Status).Scan(&rd.ID, &rd.CreatedAt, &rd.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert rider: %w", err)
	}
	return rd, nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*Rider, error) {
	var rd Rider
	err := r.db.QueryRow(ctx, `
		SELECT id, name, phone, status, created_at, updated_at
		FROM riders WHERE id = $1
	`, id).Scan(&rd.ID, &rd.Name, &rd.Phone, &rd.Status, &rd.CreatedAt, &rd.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rd, nil
}

func (r *Repository) SetStatus(ctx context.Context, id uuid.UUID, s Status) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE riders SET status = $1, updated_at = NOW() WHERE id = $2
	`, s, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveOrder returns the rider's current non-terminal order, or ErrNotFound
// if they have none. Used for /riders/:id/orders.
func (r *Repository) ActiveOrder(ctx context.Context, riderID uuid.UUID) (uuid.UUID, string, error) {
	var id uuid.UUID
	var status string
	err := r.db.QueryRow(ctx, `
		SELECT id, status FROM orders
		WHERE rider_id = $1 AND status NOT IN ('DELIVERED', 'CANCELLED')
		ORDER BY created_at DESC LIMIT 1
	`, riderID).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", ErrNotFound
	}
	return id, status, err
}
