package order

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

type Handler struct {
	svc *Service
	log zerolog.Logger
}

func NewHandler(svc *Service, log zerolog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

func (h *Handler) Routes(r chi.Router) {
	r.Route("/api/v1/orders", func(r chi.Router) {
		r.Post("/", h.place)
		r.Get("/{id}", h.get)
		r.Patch("/{id}/cancel", h.cancel)
		r.Patch("/{id}/status", h.updateStatus)
	})
}

func (h *Handler) place(w http.ResponseWriter, r *http.Request) {
	var req PlaceOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}

	o, err := h.svc.Place(r.Context(), req)
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		h.log.Error().Err(err).Msg("place order failed")
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, o)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	o, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "order not found")
			return
		}
		h.log.Error().Err(err).Msg("get order failed")
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// updateStatus handles PATCH /api/v1/orders/{id}/status. Body: {"status":"PICKED_UP"}.
// Invalid transitions return 409 Conflict.
func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Status Status `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Status == "" {
		writeErr(w, http.StatusBadRequest, "status required")
		return
	}
	o, err := h.svc.Transition(r.Context(), id, req.Status)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, "order not found")
		default:
			writeErr(w, http.StatusConflict, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	o, err := h.svc.Cancel(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeErr(w, http.StatusNotFound, "order not found")
		case errors.Is(err, ErrInvalidRequest):
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			// illegal transition returns a plain error; treat as 409 Conflict.
			writeErr(w, http.StatusConflict, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
