package order

import "fmt"

// validTransitions encodes the order lifecycle. Any state → CANCELLED is
// allowed unless already DELIVERED or CANCELLED.
var validTransitions = map[Status]map[Status]struct{}{
	StatusPlaced:    {StatusAccepted: {}, StatusCancelled: {}},
	StatusAccepted:  {StatusPreparing: {}, StatusCancelled: {}},
	StatusPreparing: {StatusPickedUp: {}, StatusCancelled: {}},
	StatusPickedUp:  {StatusDelivered: {}, StatusCancelled: {}},
	StatusDelivered: {},
	StatusCancelled: {},
}

// ValidateTransition returns an error if `to` is not reachable from `from`.
func ValidateTransition(from, to Status) error {
	allowed, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("unknown state: %s", from)
	}
	if _, ok := allowed[to]; !ok {
		return fmt.Errorf("illegal transition: %s → %s", from, to)
	}
	return nil
}
