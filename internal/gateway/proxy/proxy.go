package proxy

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/multi-ops/internal/protocol"
)

// AgentConn represents a connected agent
type AgentConn struct {
	ID   string
	Conn *websocket.Conn
	mu   sync.Mutex
}

// Manager manages agent connections and routes messages between master and agents
type Manager struct {
	agents   map[string]*AgentConn
	agentsMu sync.RWMutex

	masterConn *websocket.Conn
	masterMu   sync.Mutex
}

func NewManager() *Manager {
	return &Manager{
		agents: make(map[string]*AgentConn),
	}
}

func (m *Manager) RegisterAgent(id string, conn *websocket.Conn) {
	m.agentsMu.Lock()
	if old, ok := m.agents[id]; ok && old.Conn != conn {
		old.Conn.Close()
	}
	m.agents[id] = &AgentConn{ID: id, Conn: conn}
	m.agentsMu.Unlock()
	log.Printf("[Gateway] Agent registered: %s (total: %d)", id, m.AgentCount())
}

func (m *Manager) UnregisterAgent(id string) {
	m.agentsMu.Lock()
	delete(m.agents, id)
	m.agentsMu.Unlock()
	log.Printf("[Gateway] Agent unregistered: %s (total: %d)", id, m.AgentCount())
}

func (m *Manager) GetAgent(id string) (*AgentConn, bool) {
	m.agentsMu.RLock()
	defer m.agentsMu.RUnlock()
	a, ok := m.agents[id]
	return a, ok
}

func (m *Manager) GetAllAgentIDs() []string {
	m.agentsMu.RLock()
	defer m.agentsMu.RUnlock()
	ids := make([]string, 0, len(m.agents))
	for id := range m.agents {
		ids = append(ids, id)
	}
	return ids
}

func (m *Manager) AgentCount() int {
	m.agentsMu.RLock()
	defer m.agentsMu.RUnlock()
	return len(m.agents)
}

func (m *Manager) SetMasterConn(conn *websocket.Conn) {
	m.masterMu.Lock()
	if m.masterConn != nil {
		m.masterConn.Close()
	}
	m.masterConn = conn
	m.masterMu.Unlock()
	log.Println("[Gateway] Master connected")
}

func (m *Manager) ClearMasterConn() {
	m.masterMu.Lock()
	m.masterConn = nil
	m.masterMu.Unlock()
	log.Println("[Gateway] Master disconnected")
}

// SendToAgent sends a message to a specific agent
func (m *Manager) SendToAgent(agentID string, msg protocol.Message) error {
	m.agentsMu.RLock()
	agent, ok := m.agents[agentID]
	m.agentsMu.RUnlock()

	if !ok {
		return nil // agent not connected
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()
	return agent.Conn.WriteMessage(websocket.TextMessage, data)
}

// BroadcastToAgents sends a message to multiple agents
func (m *Manager) BroadcastToAgents(agentIDs []string, msg protocol.Message) {
	for _, id := range agentIDs {
		if err := m.SendToAgent(id, msg); err != nil {
			log.Printf("[Gateway] Failed to send to agent %s: %v", id, err)
		}
	}
}

// SendToMaster forwards a message to the master server
func (m *Manager) SendToMaster(msg protocol.Message) error {
	m.masterMu.Lock()
	defer m.masterMu.Unlock()

	if m.masterConn == nil {
		return nil
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return m.masterConn.WriteMessage(websocket.TextMessage, data)
}

// ForwardAgentToMaster reads messages from an agent and forwards to master
func (m *Manager) ForwardAgentToMaster(agentID string, conn *websocket.Conn) {
	defer func() {
		m.UnregisterAgent(agentID)
		conn.Close()
	}()

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				log.Printf("[Gateway] Agent %s read error: %v", agentID, err)
			}
			return
		}

		var msg protocol.Message
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}

		msg.AgentID = agentID

		// For registration messages, also track locally
		if msg.Type == protocol.TypeRegister {
			m.RegisterAgent(agentID, conn)
		}

		// Forward to master
		if err := m.SendToMaster(msg); err != nil {
			log.Printf("[Gateway] Failed to forward to master: %v", err)
		}
	}
}

// ForwardMasterToAgent reads messages from master and routes to appropriate agent
func (m *Manager) ForwardMasterToAgent(conn *websocket.Conn) {
	defer func() {
		m.ClearMasterConn()
		conn.Close()
	}()

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				log.Printf("[Gateway] Master read error: %v", err)
			}
			return
		}

		var msg protocol.Message
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}

		// Route to specific agent or broadcast
		if msg.AgentID != "" {
			m.SendToAgent(msg.AgentID, msg)
		}
		// For exec requests targeting multiple agents
		var req protocol.ExecRequest
		if msg.Type == protocol.TypeExecRequest && msg.Decode(&req) == nil && len(req.AgentIDs) > 0 {
			m.BroadcastToAgents(req.AgentIDs, msg)
		}
	}
}
