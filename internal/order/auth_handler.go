package order

import (
	"encoding/json"
	"net/http"

	"github.com/Kush8459/rush-delivery-engine/pkg/auth"
	"github.com/google/uuid"
)

// AuthHandler exposes a login endpoint that mints a JWT for a customer.
// There is no password verification — this is a demo project. Plug in real
// password checking against a users table when you add one.
type AuthHandler struct {
	signer *auth.Signer
}

func NewAuthHandler(signer *auth.Signer) *AuthHandler {
	return &AuthHandler{signer: signer}
}

type loginRequest struct {
	CustomerID uuid.UUID `json:"customer_id"`
}

type loginResponse struct {
	Token string `json:"token"`
}

// Login is mounted at POST /api/v1/auth/login. Accepts any customer_id and
// returns a signed JWT. In production: verify password, rate-limit, audit.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CustomerID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "customer_id required")
		return
	}
	tok, err := h.signer.Sign(req.CustomerID, "customer")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sign failed")
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{Token: tok})
}
