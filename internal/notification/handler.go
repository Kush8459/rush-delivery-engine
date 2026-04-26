package notification

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

const (
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
)

type Handler struct {
	hub      *Hub
	log      zerolog.Logger
	upgrader websocket.Upgrader
}

func NewHandler(hub *Hub, log zerolog.Logger) *Handler {
	return &Handler{
		hub: hub,
		log: log,
		upgrader: websocket.Upgrader{
			CheckOrigin:     func(r *http.Request) bool { return true },
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}
}

func (h *Handler) Routes(r chi.Router) {
	r.Get("/ws/order/{order_id}", h.order)
}

// order subscribes a customer to updates for a single order.
// The connection is read-only from our perspective — we just need ping/pong
// to detect dead clients and drop them.
func (h *Handler) order(w http.ResponseWriter, r *http.Request) {
	orderID, err := uuid.Parse(chi.URLParam(r, "order_id"))
	if err != nil {
		http.Error(w, "invalid order_id", http.StatusBadRequest)
		return
	}
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Error().Err(err).Msg("ws upgrade failed")
		return
	}
	defer conn.Close()

	log := h.log.With().Str("order_id", orderID.String()).Logger()
	h.hub.Register(orderID, conn)
	defer h.hub.Unregister(orderID, conn)

	log.Info().Msg("customer ws connected")
	defer log.Info().Msg("customer ws disconnected")

	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	// Read loop exists purely to detect disconnects. Clients shouldn't send data.
	for {
		if _, _, err := conn.NextReader(); err != nil {
			return
		}
	}
}
