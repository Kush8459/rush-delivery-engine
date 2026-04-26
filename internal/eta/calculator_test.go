package eta

import "testing"

func TestCompute_ZeroDistanceReturnsOneMinute(t *testing.T) {
	// We round up and floor to 1 so "0.3 min away" doesn't look like "here".
	got := Compute(28.6139, 77.2090, 28.6139, 77.2090)
	if got != 1 {
		t.Errorf("expected 1 (minimum), got %d", got)
	}
}

func TestCompute_ScalesWithDistance(t *testing.T) {
	// Move dropoff progressively further → ETA must be monotonically non-decreasing.
	near := Compute(28.6139, 77.2090, 28.6145, 77.2095)  // ~80m
	mid := Compute(28.6139, 77.2090, 28.6200, 77.2150)    // ~800m
	far := Compute(28.6139, 77.2090, 28.7000, 77.3000)    // ~14km

	if !(near <= mid && mid <= far) {
		t.Errorf("ETA not monotonic: near=%d mid=%d far=%d", near, mid, far)
	}
	// Sanity: the "far" case should be many minutes at 25 km/h.
	if far < 10 {
		t.Errorf("14km far case should take >10 min, got %d", far)
	}
}

func TestCompute_RoundsUp(t *testing.T) {
	// At 25 km/h average: 1 km = 2.4 min → should round up to 3.
	got := Compute(28.6139, 77.2090, 28.6229, 77.2090) // ~1 km north
	if got < 2 || got > 4 {
		t.Errorf("expected ~2-3 min for ~1 km, got %d", got)
	}
}
