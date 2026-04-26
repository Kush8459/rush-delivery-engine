package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims carries the minimum identity we need downstream: a customer UUID
// and a human-readable role. Rider-facing APIs can use a different Role.
type Claims struct {
	CustomerID uuid.UUID `json:"cid"`
	Role       string    `json:"role"`
	jwt.RegisteredClaims
}

type Signer struct {
	secret []byte
	ttl    time.Duration
}

func NewSigner(secret string, ttlHours int) *Signer {
	if ttlHours <= 0 {
		ttlHours = 24
	}
	return &Signer{
		secret: []byte(secret),
		ttl:    time.Duration(ttlHours) * time.Hour,
	}
}

// Sign issues a new HS256 token for the given customer ID.
func (s *Signer) Sign(customerID uuid.UUID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		CustomerID: customerID,
		Role:       role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
			Issuer:    "rush-delivery-engine",
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(s.secret)
}

// Verify parses and validates a token string. Returns the embedded claims or
// a non-nil error if the token is malformed, expired, or signed with the
// wrong secret.
func (s *Signer) Verify(tokenStr string) (*Claims, error) {
	c := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
		return nil, errors.New("token invalid")
	}
	return c, nil
}
