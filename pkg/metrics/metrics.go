package metrics

import (
	"bufio"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// httpRequestsTotal counts completed requests by method, path pattern, and status.
// Path comes from chi's RoutePattern so `/orders/{id}` doesn't explode cardinality.
var httpRequestsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests processed, by method / route / status.",
	},
	[]string{"service", "method", "route", "status"},
)

// httpRequestDuration measures request latency buckets (ms). Default buckets
// from Prometheus default work well for typical API latencies.
var httpRequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"service", "method", "route"},
)

// businessEvents counts domain events (orders placed, orders assigned, etc).
// Services increment these in their service layer, not middleware.
var businessEvents = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "business_events_total",
		Help: "Count of domain-level events (orders placed, assigned, etc).",
	},
	[]string{"event"},
)

// Handler returns the /metrics endpoint for Prometheus to scrape.
func Handler() http.Handler { return promhttp.Handler() }

// IncEvent bumps a business event counter. Safe to call from any service.
func IncEvent(event string) {
	businessEvents.WithLabelValues(event).Inc()
}

// statusRecorder wraps ResponseWriter to capture the status code.
// Must implement http.Hijacker so WebSocket upgrades (gorilla/websocket) can
// take over the underlying connection — without this, `/ws/...` handlers 500.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// HTTPMiddleware records request count + duration using chi's route pattern.
// Pass the chi route-pattern extractor so cardinality stays bounded.
func HTTPMiddleware(service string, routePattern func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sr := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(sr, r)

			route := routePattern(r)
			if route == "" {
				route = "unknown"
			}
			elapsed := time.Since(start).Seconds()

			httpRequestDuration.WithLabelValues(service, r.Method, route).Observe(elapsed)
			httpRequestsTotal.
				WithLabelValues(service, r.Method, route, strconv.Itoa(sr.status)).
				Inc()
		})
	}
}
