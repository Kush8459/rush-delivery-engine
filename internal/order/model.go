package order

import (
	"time"

	"github.com/google/uuid"
)

// Status is the order lifecycle state. Transitions are governed by statemachine.go.
type Status string

const (
	StatusPlaced    Status = "PLACED"
	StatusAccepted  Status = "ACCEPTED"
	StatusPreparing Status = "PREPARING"
	StatusPickedUp  Status = "PICKED_UP"
	StatusDelivered Status = "DELIVERED"
	StatusCancelled Status = "CANCELLED"
)

type Order struct {
	ID              uuid.UUID  `json:"id"`
	CustomerID      uuid.UUID  `json:"customer_id"`
	RiderID         *uuid.UUID `json:"rider_id,omitempty"`
	RestaurantID    uuid.UUID  `json:"restaurant_id"`
	Status          Status     `json:"status"`
	TotalAmount     float64    `json:"total_amount"`
	DeliveryAddress string     `json:"delivery_address"`
	PickupLat       float64    `json:"pickup_lat"`
	PickupLng       float64    `json:"pickup_lng"`
	DropoffLat      float64    `json:"dropoff_lat"`
	DropoffLng      float64    `json:"dropoff_lng"`
	ETAMinutes      *int       `json:"eta_minutes,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// PlaceOrderRequest is the inbound HTTP payload for POST /orders.
type PlaceOrderRequest struct {
	CustomerID      uuid.UUID `json:"customer_id"`
	RestaurantID    uuid.UUID `json:"restaurant_id"`
	TotalAmount     float64   `json:"total_amount"`
	DeliveryAddress string    `json:"delivery_address"`
	PickupLat       float64   `json:"pickup_lat"`
	PickupLng       float64   `json:"pickup_lng"`
	DropoffLat      float64   `json:"dropoff_lat"`
	DropoffLng      float64   `json:"dropoff_lng"`
}

// OrderPlacedEvent is the Kafka payload for topic `order.placed`.
// Consumed by assignment-service to trigger rider selection.
type OrderPlacedEvent struct {
	OrderID      uuid.UUID `json:"order_id"`
	CustomerID   uuid.UUID `json:"customer_id"`
	RestaurantID uuid.UUID `json:"restaurant_id"`
	PickupLat    float64   `json:"pickup_lat"`
	PickupLng    float64   `json:"pickup_lng"`
	DropoffLat   float64   `json:"dropoff_lat"`
	DropoffLng   float64   `json:"dropoff_lng"`
	PlacedAt     time.Time `json:"placed_at"`
}
