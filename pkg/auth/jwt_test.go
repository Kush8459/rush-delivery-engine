package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestSignAndVerify_RoundTrip(t *testing.T) {
	signer := NewSigner("test-secret", 1)
	cid := uuid.New()

	tok, err := signer.Sign(cid, "customer")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}

	claims, err := signer.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.CustomerID != cid {
		t.Errorf("customer id mismatch: got %v want %v", claims.CustomerID, cid)
	}
	if claims.Role != "customer" {
		t.Errorf("role mismatch: got %q", claims.Role)
	}
}

func TestVerify_RejectsWrongSecret(t *testing.T) {
	signer := NewSigner("right-secret", 1)
	attacker := NewSigner("wrong-secret", 1)

	tok, _ := signer.Sign(uuid.New(), "customer")
	if _, err := attacker.Verify(tok); err == nil {
		t.Fatal("token signed with different secret should fail verification")
	}
}

func TestVerify_RejectsExpiredToken(t *testing.T) {
	// Build a token by hand with an already-expired exp claim. The signer API
	// only allows positive TTLs so we construct one directly here.
	claims := Claims{
		CustomerID: uuid.New(),
		Role:       "customer",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		},
	}
	t2 := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok, err := t2.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}

	signer := NewSigner("test-secret", 1)
	if _, err := signer.Verify(tok); err == nil {
		t.Fatal("expired token should fail verification")
	}
}

func TestVerify_RejectsMalformedToken(t *testing.T) {
	signer := NewSigner("test-secret", 1)
	if _, err := signer.Verify("not.a.token"); err == nil {
		t.Fatal("malformed token should fail verification")
	}
}
