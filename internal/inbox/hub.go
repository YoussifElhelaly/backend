package inbox

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type WSEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

type wsConn struct {
	ws       *websocket.Conn
	tenantID string
}

type Hub struct {
	mu    sync.RWMutex
	conns map[string]map[*wsConn]bool
}

var GlobalHub = &Hub{
	conns: make(map[string]map[*wsConn]bool),
}

func (h *Hub) register(c *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[c.tenantID] == nil {
		h.conns[c.tenantID] = make(map[*wsConn]bool)
	}
	h.conns[c.tenantID][c] = true
}

func (h *Hub) unregister(c *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns[c.tenantID], c)
	// Clean up empty tenant bucket to prevent memory leak.
	if len(h.conns[c.tenantID]) == 0 {
		delete(h.conns, c.tenantID)
	}
}

func (h *Hub) Broadcast(tenantID string, event WSEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	h.mu.RLock()
	conns := make([]*wsConn, 0, len(h.conns[tenantID]))
	for c := range h.conns[tenantID] {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	for _, c := range conns {
		go func(c *wsConn) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
				slog.Debug("ws write error", "tenant_id", tenantID, "error", err)
			}
		}(c)
	}
}

func (h *Hub) SendTo(c *wsConn, event WSEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.ws.Write(ctx, websocket.MessageText, data)
}
