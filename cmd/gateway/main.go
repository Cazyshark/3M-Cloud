package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multi-ops/internal/gateway/auth"
	"github.com/multi-ops/internal/gateway/proxy"
	"github.com/multi-ops/internal/protocol"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false
		}
		host := r.Host
		return origin == "http://"+host || origin == "https://"+host ||
			strings.HasPrefix(origin, "https://"+host) || strings.HasPrefix(origin, "http://"+host)
	},
}

func main() {
	listen := getEnv("GATEWAY_LISTEN", ":8081")
	masterURL := getEnv("MASTER_WS_URL", "ws://localhost:8080/ws/gateway")
	tokens := getEnvSlice("AGENT_TOKENS", "")
	masterSecret := getEnv("MASTER_SECRET", "")

	// Auto-generate secure defaults if not provided
	if len(tokens) == 0 {
		token := auth.GenerateDefaultToken()
		tokens = []string{token}
		log.Printf("[Gateway] No AGENT_TOKENS set, generated secure default token: %s", token)
		log.Printf("[Gateway] Set AGENT_TOKENS to control which tokens are accepted.")
	}
	if masterSecret == "" {
		masterSecret = auth.GenerateDefaultToken()
		log.Printf("[Gateway] No MASTER_SECRET set, generated secure default: %s", masterSecret)
		log.Printf("[Gateway] Set MASTER_SECRET to control gateway-to-master authentication.")
	}

	authenticator := auth.New(tokens)
	manager := proxy.NewManager()

	// Agent connects here
	http.HandleFunc("/connect", func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agent_id")
		token := r.URL.Query().Get("token")

		if agentID == "" {
			http.Error(w, "agent_id required", http.StatusBadRequest)
			return
		}

		if !authenticator.ValidateAgent(agentID, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[Gateway] Upgrade failed: %v", err)
			return
		}

		log.Printf("[Gateway] Agent connected: %s", agentID)
		manager.RegisterAgent(agentID, conn)
		go manager.ForwardAgentToMaster(agentID, conn)
	})

	// Master can also connect to gateway directly (alternative path)
	http.HandleFunc("/master", func(w http.ResponseWriter, r *http.Request) {
		if !authenticator.ValidateMaster(r, masterSecret) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[Gateway] Master upgrade failed: %v", err)
			return
		}

		manager.SetMasterConn(conn)
		go manager.ForwardMasterToAgent(conn)
	})

	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		agents := manager.GetAllAgentIDs()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"status":"ok","agent_count":%d}`, len(agents))))
	})

	// Start gateway HTTP server
	log.Printf("[Gateway] Starting gateway server on %s", listen)
	go func() {
		if err := http.ListenAndServe(listen, nil); err != nil {
			log.Fatalf("[Gateway] Server error: %v", err)
		}
	}()

	// Connect to master server
	go connectToMaster(masterURL, masterSecret, manager)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("[Gateway] Shutting down...")
}

func connectToMaster(masterURL, secret string, manager *proxy.Manager) {
	for {
		header := http.Header{}
		if secret != "" {
			header.Set("X-Master-Secret", secret)
		}

		conn, _, err := websocket.DefaultDialer.Dial(masterURL, header)
		if err != nil {
			log.Printf("[Gateway] Failed to connect to master: %v, retrying in 5s...", err)
			time.Sleep(5 * time.Second)
			continue
		}

		log.Printf("[Gateway] Connected to master at %s", masterURL)
		manager.SetMasterConn(conn)

		// Re-send all connected agent registrations
		for _, agentID := range manager.GetAllAgentIDs() {
			regMsg, _ := protocol.NewMessage(protocol.TypeRegister, agentID,
				protocol.AgentInfo{AgentID: agentID, Status: "online"})
			manager.SendToMaster(regMsg)
		}

		// Forward messages from master to agents
		for {
			_, msgData, err := conn.ReadMessage()
			if err != nil {
				log.Printf("[Gateway] Master connection lost: %v", err)
				break
			}

			var msg protocol.Message
			if err := json.Unmarshal(msgData, &msg); err != nil {
				continue
			}

			// Route to specific agent
			if msg.AgentID != "" {
				manager.SendToAgent(msg.AgentID, msg)
			}
		}

		manager.ClearMasterConn()
		conn.Close()

		log.Println("[Gateway] Reconnecting to master in 5s...")
		time.Sleep(5 * time.Second)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvSlice(key string, fallback string) []string {
	v := os.Getenv(key)
	if v == "" {
		if fallback != "" {
			return strings.Split(fallback, ",")
		}
		return nil
	}
	return strings.Split(v, ",")
}
