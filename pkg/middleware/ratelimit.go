package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// SlidingWindow is a Redis-backed sliding window limiter.
//
// Algorithm: for key K and window W seconds,
//   1. ZADD  K <now-ns> <now-ns>          (member = score = timestamp)
//   2. ZREMRANGEBYSCORE K 0 (now-ns - W)  (drop stale entries)
//   3. ZCARD K                            (count of requests in window)
//   4. EXPIRE K W                         (reap idle keys)
// Runs as a pipeline so the round-trip cost is one RTT.
type SlidingWindow struct {
	rdb    *redis.Client
	limit  int
	window time.Duration
	prefix string
}

func NewSlidingWindow(rdb *redis.Client, limit int, window time.Duration, prefix string) *SlidingWindow {
	return &SlidingWindow{rdb: rdb, limit: limit, window: window, prefix: prefix}
}

// Allow returns true if the caller is under the limit. identity is whatever
// scope you're throttling (customer_id, IP, API key, etc).
func (s *SlidingWindow) Allow(ctx context.Context, identity string) (bool, int, error) {
	key := s.prefix + ":" + identity
	now := time.Now().UnixNano()
	cutoff := now - s.window.Nanoseconds()

	pipe := s.rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: strconv.FormatInt(now, 10)})
	pipe.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(cutoff, 10))
	card := pipe.ZCard(ctx, key)
	pipe.Expire(ctx, key, s.window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, err
	}

	count := int(card.Val())
	return count <= s.limit, count, nil
}

// HTTP returns a chi/net-http middleware that calls identify(r) to derive the
// throttle key. If identify returns "", the request is passed through unthrottled
// (useful for health checks or unauthenticated probes).
func (s *SlidingWindow) HTTP(log zerolog.Logger, identify func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := identify(r)
			if id == "" {
				next.ServeHTTP(w, r)
				return
			}
			ok, count, err := s.Allow(r.Context(), id)
			if err != nil {
				// Fail open: a broken Redis shouldn't 500 all traffic.
				log.Warn().Err(err).Msg("rate limiter unavailable; allowing")
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(s.limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(max0(s.limit-count)))
			if !ok {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(s.window.Seconds())))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate limit exceeded"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
