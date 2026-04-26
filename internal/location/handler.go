package location

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

// Protocol notes:
//   client → server: {"type":"location_update","payload":{"lat":..,"lng":..,"timestamp":".."}}
//   server → client: {"type":"ack"} after each accepted update, or
//                    {"type":"error","payload":{"message":".."}} on failure.

const (
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
	writeWait  = 10 * time.Second
)

type Handler struct {
	svc      *Service
	log      zerolog.Logger
	upgrader websocket.Upgrader
}

func NewHandler(svc *Service, log zerolog.Logger) *Handler {
	return &Handler{
		svc: svc,
		log: log,
		upgrader: websocket.Upgrader{
			// For local dev; replace with real origin check in production.
			CheckOrigin:     func(r *http.Request) bool { return true },
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}
}

func (h *Handler) Routes(r chi.Router) {
	r.Get("/ws/rider/{rider_id}", h.rider)
}

func (h *Handler) rider(w http.ResponseWriter, r *http.Request) {
	riderID, err := uuid.Parse(chi.URLParam(r, "rider_id"))
	if err != nil {
		http.Error(w, "invalid rider_id", http.StatusBadRequest)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Error().Err(err).Msg("ws upgrade failed")
		return
	}
	defer conn.Close()

	log := h.log.With().Str("rider_id", riderID.String()).Logger()
	log.Info().Msg("rider ws connected")
	defer log.Info().Msg("rider ws disconnected")

	// Heartbeat: clients must respond to pings within pongWait.
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Ping ticker runs in a goroutine.
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	for {
		var msg IncomingMessage
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Warn().Err(err).Msg("unexpected ws close")
			}
			return
		}

		if msg.Type != "location_update" {
			writeWSError(conn, "unknown message type")
			continue
		}
		lat, ok1 := asFloat(msg.Payload["lat"])
		lng, ok2 := asFloat(msg.Payload["lng"])
		if !ok1 || !ok2 {
			writeWSError(conn, "lat/lng required")
			continue
		}
		ts := time.Now().UTC()
		if raw, ok := msg.Payload["timestamp"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				ts = parsed
			}
		}

		if err := h.svc.UpdateLocation(r.Context(), riderID, lat, lng, ts); err != nil {
			log.Error().Err(err).Msg("update location failed")
			writeWSError(conn, "update failed")
			continue
		}
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		_ = conn.WriteJSON(map[string]string{"type": "ack"})
	}
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func writeWSError(conn *websocket.Conn, msg string) {
	conn.SetWriteDeadline(time.Now().Add(writeWait))
	_ = conn.WriteJSON(map[string]any{"type": "error", "payload": map[string]string{"message": msg}})
}
