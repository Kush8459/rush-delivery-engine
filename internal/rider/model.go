package rider

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusOffline   Status = "OFFLINE"
	StatusAvailable Status = "AVAILABLE"
	StatusBusy      Status = "BUSY"
)

func (s Status) Valid() bool {
	switch s {
	case StatusOffline, StatusAvailable, StatusBusy:
		return true
	}
	return false
}

type Rider struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Phone     string    `json:"phone"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type RegisterRequest struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

// SetStatusRequest toggles a rider's shift status. When going AVAILABLE, the
// client should include current coordinates so we can seed Redis geo.
type SetStatusRequest struct {
	Status Status   `json:"status"`
	Lat    *float64 `json:"lat,omitempty"`
	Lng    *float64 `json:"lng,omitempty"`
}
