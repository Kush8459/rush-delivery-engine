package order

import "testing"

func TestValidateTransition_HappyPath(t *testing.T) {
	happy := []struct {
		from, to Status
	}{
		{StatusPlaced, StatusAccepted},
		{StatusAccepted, StatusPreparing},
		{StatusPreparing, StatusPickedUp},
		{StatusPickedUp, StatusDelivered},
	}
	for _, c := range happy {
		if err := ValidateTransition(c.from, c.to); err != nil {
			t.Errorf("%s → %s should be allowed, got: %v", c.from, c.to, err)
		}
	}
}

func TestValidateTransition_CancellationAllowedFromActiveStates(t *testing.T) {
	active := []Status{StatusPlaced, StatusAccepted, StatusPreparing, StatusPickedUp}
	for _, s := range active {
		if err := ValidateTransition(s, StatusCancelled); err != nil {
			t.Errorf("%s → CANCELLED should be allowed, got: %v", s, err)
		}
	}
}

func TestValidateTransition_TerminalStatesReject(t *testing.T) {
	cases := []struct {
		from, to Status
	}{
		{StatusDelivered, StatusCancelled}, // can't cancel a delivered order
		{StatusDelivered, StatusPickedUp},  // can't rewind
		{StatusCancelled, StatusPlaced},    // can't un-cancel
	}
	for _, c := range cases {
		if err := ValidateTransition(c.from, c.to); err == nil {
			t.Errorf("%s → %s should be rejected", c.from, c.to)
		}
	}
}

func TestValidateTransition_IllegalSkipsRejected(t *testing.T) {
	// Can't skip states: PLACED → PICKED_UP without going through ACCEPTED, PREPARING.
	if err := ValidateTransition(StatusPlaced, StatusPickedUp); err == nil {
		t.Error("PLACED → PICKED_UP should be rejected (skips states)")
	}
	if err := ValidateTransition(StatusAccepted, StatusDelivered); err == nil {
		t.Error("ACCEPTED → DELIVERED should be rejected (skips states)")
	}
}

func TestValidateTransition_UnknownFromState(t *testing.T) {
	if err := ValidateTransition(Status("BOGUS"), StatusPlaced); err == nil {
		t.Error("unknown source state should produce error")
	}
}
