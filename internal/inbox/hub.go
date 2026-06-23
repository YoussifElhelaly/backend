package inbox

import (
	"context"
	"encoding/json"
	"log"
	"sync"

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
			if err := c.ws.Write(context.Background(), websocket.MessageText, data); err != nil {
				log.Printf("ws write error: %v", err)
			}
		}(c)
	}
}
