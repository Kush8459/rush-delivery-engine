package notification

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

// Hub tracks customer WebSocket subscriptions by order_id and fans out
// events as they arrive. One customer can have multiple tabs → slice of conns.
//
// Concurrency: a single RWMutex guards the map. Writes happen on connect/
// disconnect (rare); Broadcast takes an RLock and releases it before writing
// so a slow client can't block the fan-out.
type Hub struct {
	mu    sync.RWMutex
	conns map[uuid.UUID]map[*websocket.Conn]struct{}
	log   zerolog.Logger
}

func NewHub(log zerolog.Logger) *Hub {
	return &Hub{
		conns: make(map[uuid.UUID]map[*websocket.Conn]struct{}),
		log:   log,
	}
}

func (h *Hub) Register(orderID uuid.UUID, c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.conns[orderID]; !ok {
		h.conns[orderID] = make(map[*websocket.Conn]struct{})
	}
	h.conns[orderID][c] = struct{}{}
}

func (h *Hub) Unregister(orderID uuid.UUID, c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.conns[orderID]
	if !ok {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(h.conns, orderID)
	}
}

// Broadcast sends v to every client subscribed to orderID.
// A write failure drops the connection (the read loop in handler will also
// notice it and Unregister on its side).
func (h *Hub) Broadcast(orderID uuid.UUID, v any) {
	h.mu.RLock()
	set := h.conns[orderID]
	conns := make([]*websocket.Conn, 0, len(set))
	for c := range set {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	for _, c := range conns {
		c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := c.WriteJSON(v); err != nil {
			h.log.Warn().Err(err).Str("order_id", orderID.String()).Msg("broadcast failed")
			_ = c.Close()
		}
	}
}
