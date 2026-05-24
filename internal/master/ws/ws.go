package ws

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/multi-ops/internal/protocol"
)

// Hub manages browser WebSocket connections
type Hub struct {
	clients   map[string]*Client
	clientsMu sync.RWMutex
}

type Client struct {
	ID   string
	Conn *websocket.Conn
	mu   sync.Mutex
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[string]*Client),
	}
}

func (h *Hub) Register(client *Client) {
	h.clientsMu.Lock()
	h.clients[client.ID] = client
	h.clientsMu.Unlock()
	log.Printf("[WS Hub] Client connected: %s (total: %d)", client.ID, h.ClientCount())
}

func (h *Hub) Unregister(clientID string) {
	h.clientsMu.Lock()
	delete(h.clients, clientID)
	h.clientsMu.Unlock()
	log.Printf("[WS Hub] Client disconnected: %s", clientID)
}

func (h *Hub) ClientCount() int {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()
	return len(h.clients)
}

func (h *Hub) Broadcast(msg interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()

	for _, c := range h.clients {
		c.mu.Lock()
		c.Conn.WriteMessage(websocket.TextMessage, data)
		c.mu.Unlock()
	}
}

func (h *Hub) SendToClient(clientID string, msg interface{}) error {
	h.clientsMu.RLock()
	c, ok := h.clients[clientID]
	h.clientsMu.RUnlock()

	if !ok {
		return nil
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteMessage(websocket.TextMessage, data)
}

// GatewayConn manages the outgoing WebSocket connection to the gateway.
// Only Send is used — reading is handled by the api.Handler in a dedicated goroutine.
type GatewayConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func NewGatewayConn() *GatewayConn {
	return &GatewayConn{}
}

func (g *GatewayConn) SetConn(conn *websocket.Conn) {
	g.mu.Lock()
	if g.conn != nil {
		g.conn.Close()
	}
	g.conn = conn
	g.mu.Unlock()
}

func (g *GatewayConn) ClearConn() {
	g.mu.Lock()
	g.conn = nil
	g.mu.Unlock()
}

func (g *GatewayConn) Send(msg protocol.Message) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.conn == nil {
		return nil
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return g.conn.WriteMessage(websocket.TextMessage, data)
}

func (g *GatewayConn) IsConnected() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.conn != nil
}
