package eta

import (
	"math"

	"github.com/Kush8459/rush-delivery-engine/pkg/geo"
)

// AvgRiderSpeedKmh is the heuristic speed we use for ETA.
// Real systems plug in OSRM / Google Directions; this is a stand-in.
const AvgRiderSpeedKmh = 25.0

// Compute returns ETA in whole minutes from (fromLat,fromLng) to (toLat,toLng).
// Rounded up so a 0.3-minute result doesn't become "0 minutes away".
func Compute(fromLat, fromLng, toLat, toLng float64) int {
	km := geo.HaversineKm(fromLat, fromLng, toLat, toLng)
	hours := km / AvgRiderSpeedKmh
	mins := hours * 60
	if mins < 1 {
		return 1
	}
	return int(math.Ceil(mins))
}
