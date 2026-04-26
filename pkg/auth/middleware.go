package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type ctxKey int

const claimsKey ctxKey = iota

// FromContext returns the Claims attached by Middleware, or nil if none.
func FromContext(ctx context.Context) *Claims {
	if c, ok := ctx.Value(claimsKey).(*Claims); ok {
		return c
	}
	return nil
}

// Middleware extracts a `Bearer <token>` header, validates it, and attaches
// the claims to the request context. Requests without a valid token get 401.
// Use selectively — don't wrap health/metrics endpoints with this.
func Middleware(signer *Signer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hdr := r.Header.Get("Authorization")
			if !strings.HasPrefix(hdr, "Bearer ") {
				writeUnauth(w, "missing bearer token")
				return
			}
			token := strings.TrimPrefix(hdr, "Bearer ")
			claims, err := signer.Verify(token)
			if err != nil {
				writeUnauth(w, "invalid token")
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeUnauth(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
