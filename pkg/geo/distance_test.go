package geo

import (
	"math"
	"testing"
)

func TestHaversineKm_ZeroForSamePoint(t *testing.T) {
	got := HaversineKm(28.6139, 77.2090, 28.6139, 77.2090)
	if got > 1e-6 {
		t.Errorf("expected ~0, got %v", got)
	}
}

func TestHaversineKm_KnownDistance(t *testing.T) {
	// Delhi (Connaught Place) → Mumbai (CST) is ~1154 km along a great circle.
	got := HaversineKm(28.6315, 77.2167, 18.9398, 72.8355)
	// Accept ±2% tolerance — haversine assumes perfect sphere.
	if math.Abs(got-1154) > 1154*0.02 {
		t.Errorf("expected ~1154 km, got %v", got)
	}
}

func TestHaversineKm_Symmetric(t *testing.T) {
	a := HaversineKm(28.6, 77.2, 18.9, 72.8)
	b := HaversineKm(18.9, 72.8, 28.6, 77.2)
	if math.Abs(a-b) > 1e-9 {
		t.Errorf("haversine should be symmetric, got %v vs %v", a, b)
	}
}

func TestHaversineKm_AntipodalBounded(t *testing.T) {
	// Earth circumference / 2 ≈ 20015 km. Any two points can't exceed this.
	got := HaversineKm(0, 0, 0, 180)
	if got < 20000 || got > 20100 {
		t.Errorf("antipodal along equator should be ~20015 km, got %v", got)
	}
}
