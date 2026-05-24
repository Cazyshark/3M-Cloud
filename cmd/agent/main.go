package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multi-ops/internal/agent/executor"
	"github.com/multi-ops/internal/agent/system"
	"github.com/multi-ops/internal/agent/terminal"
	"github.com/multi-ops/internal/protocol"
)

type Agent struct {
	ID        string
	Gateway   string
	Token     string
	conn      *websocket.Conn
	connMu    sync.Mutex
	sessions  map[string]*terminal.Session
	sessionsMu sync.Mutex
	stopCh    chan struct{}
}

func main() {
	agentID := getEnv("AGENT_ID", generateID())
	gateway := getEnv("GATEWAY_URL", "ws://localhost:8081/connect")
	token := getEnv("AGENT_TOKEN", "")

	agent := &Agent{
		ID:       agentID,
		Gateway:  gateway,
		Token:    token,
		sessions: make(map[string]*terminal.Session),
		stopCh:   make(chan struct{}),
	}

	log.Printf("[Agent] Starting agent %s, connecting to %s", agentID, gateway)

	go agent.connectLoop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("[Agent] Shutting down...")
	close(agent.stopCh)
}

func (a *Agent) connectLoop() {
	for {
		select {
		case <-a.stopCh:
			return
		default:
		}

		err := a.connect()
		if err != nil {
			log.Printf("[Agent] Connection error: %v, retrying in 5s...", err)
			time.Sleep(5 * time.Second)
			continue
		}

		select {
		case <-a.stopCh:
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (a *Agent) connect() error {
	u, err := url.Parse(a.Gateway)
	if err != nil {
		return fmt.Errorf("invalid gateway URL: %w", err)
	}

	q := u.Query()
	q.Set("agent_id", a.ID)
	if a.Token != "" {
		q.Set("token", a.Token)
	}
	u.RawQuery = q.Encode()

	header := http.Header{}
	header.Set("X-Agent-ID", a.ID)

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}

	a.connMu.Lock()
	a.conn = conn
	a.connMu.Unlock()

	log.Println("[Agent] Connected to gateway")

	a.register()

	done := make(chan struct{})

	// Heartbeat + metrics push
	go func() {
		heartbeatTicker := time.NewTicker(30 * time.Second)
		metricsTicker := time.NewTicker(5 * time.Second)
		defer func() {
			heartbeatTicker.Stop()
			metricsTicker.Stop()
		}()
		for {
			select {
			case <-heartbeatTicker.C:
				a.register()
			case <-metricsTicker.C:
				a.pushMetrics()
			case <-done:
				return
			}
		}
	}()

	defer func() {
		close(done)
		conn.Close()
	}()

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}

		var msg protocol.Message
		if err := json.Unmarshal(msgData, &msg); err != nil {
			log.Printf("[Agent] Invalid message: %v", err)
			continue
		}

		go a.handleMessage(msg)
	}
}

func (a *Agent) register() {
	info := system.CollectAgentInfo(a.ID)
	msg, err := protocol.NewMessage(protocol.TypeRegister, a.ID, info)
	if err != nil {
		log.Printf("[Agent] Failed to create register message: %v", err)
		return
	}
	a.send(msg)
	log.Printf("[Agent] Registered: %s (%s) %s", info.Hostname, info.PublicIP, info.Location)
}

func (a *Agent) pushMetrics() {
	metrics := system.CollectMetrics()
	metrics.Timestamp = time.Now().Unix()
	msg, _ := protocol.NewMessage(protocol.TypeMetrics, a.ID, metrics)
	a.send(msg)
}

func (a *Agent) handleMessage(msg protocol.Message) {
	switch msg.Type {
	case protocol.TypeTerminalInput:
		var input protocol.TerminalInput
		if err := msg.Decode(&input); err != nil {
			return
		}
		a.handleTerminalInput(input)

	case protocol.TypeTerminalResize:
		var resize protocol.TerminalResize
		if err := msg.Decode(&resize); err != nil {
			return
		}
		a.handleTerminalResize(resize)

	case protocol.TypeExecRequest:
		var req protocol.ExecRequest
		if err := msg.Decode(&req); err != nil {
			return
		}
		a.handleExecRequest(msg.ReqID, req)

	case protocol.TypeFileUpload:
		var req protocol.FileUpload
		if err := msg.Decode(&req); err != nil {
			return
		}
		a.handleFileUpload(msg.ReqID, req)

	case protocol.TypeFileDownload:
		var req protocol.FileDownload
		if err := msg.Decode(&req); err != nil {
			return
		}
		a.handleFileDownload(msg.ReqID, req)

	case protocol.TypeCommand:
		var cmd protocol.Command
		if err := msg.Decode(&cmd); err != nil {
			return
		}
		a.handleCommand(cmd)

	default:
		log.Printf("[Agent] Unknown message type: %s", msg.Type)
	}
}

func (a *Agent) handleTerminalInput(input protocol.TerminalInput) {
	a.sessionsMu.Lock()
	sess, ok := a.sessions[input.SessionID]
	a.sessionsMu.Unlock()

	if !ok {
		var err error
		sess, err = terminal.NewSession(input.SessionID, 80, 24)
		if err != nil {
			log.Printf("[Agent] Failed to create terminal session: %v", err)
			return
		}
		a.sessionsMu.Lock()
		a.sessions[input.SessionID] = sess
		a.sessionsMu.Unlock()

		outputCh := make(chan []byte, 100)
		go sess.StreamOutput(outputCh)
		go func() {
			for data := range outputCh {
				outMsg, _ := protocol.NewMessage(protocol.TypeTerminalOutput, a.ID,
					protocol.TerminalOutput{
						SessionID: input.SessionID,
						Data:      string(data),
					})
				a.send(outMsg)
			}
		}()
	}

	if input.Data == "\x04" {
		sess.Close()
		a.sessionsMu.Lock()
		delete(a.sessions, input.SessionID)
		a.sessionsMu.Unlock()
		return
	}

	sess.WriteInput([]byte(input.Data))
}

func (a *Agent) handleTerminalResize(resize protocol.TerminalResize) {
	a.sessionsMu.Lock()
	sess, ok := a.sessions[resize.SessionID]
	a.sessionsMu.Unlock()

	if ok {
		sess.Resize(resize.Cols, resize.Rows)
	}
}

func (a *Agent) handleExecRequest(reqID string, req protocol.ExecRequest) {
	log.Printf("[Agent] Executing script (timeout=%ds)", req.Timeout)

	result := executor.RunScript(nil, req.Script, req.Timeout, nil)

	resp, _ := protocol.NewMessage(protocol.TypeExecResponse, a.ID,
		protocol.ExecResponse{
			AgentID:  a.ID,
			ExitCode: result.ExitCode,
			Output:   result.Output,
			Error:    result.Error,
		})
	resp.ReqID = reqID
	a.send(resp)
}

func (a *Agent) handleFileUpload(reqID string, req protocol.FileUpload) {
	log.Printf("[Agent] File upload: %s (%d bytes)", req.Path, len(req.Content))

	resp := protocol.FileUploadResp{AgentID: a.ID, Path: req.Path}

	// Sanitize path: reject absolute paths outside /tmp and path traversal
	cleanPath := filepath.Clean(req.Path)
	if !strings.HasPrefix(cleanPath, "/tmp/") && !strings.HasPrefix(cleanPath, "/opt/multi-ops/") {
		resp.Error = "path must be under /tmp/ or /opt/multi-ops/"
		a.sendFileUploadResp(reqID, resp)
		return
	}

	data := []byte(req.Content)

	if _, err := os.Stat(cleanPath); err == nil && !req.Overwrite {
		resp.Error = "file already exists"
		a.sendFileUploadResp(reqID, resp)
		return
	}

	if err := os.WriteFile(cleanPath, data, 0644); err != nil {
		resp.Error = err.Error()
		a.sendFileUploadResp(reqID, resp)
		return
	}

	if req.Mode != "" {
		if mode, err := strconv.ParseUint(req.Mode, 8, 32); err == nil {
			// Reject setuid/setgid/sticky bits — only allow basic permission bits
			if mode&0o7000 != 0 {
				log.Printf("[Agent] Rejected file mode %s: setuid/setgid/sticky bits not allowed", req.Mode)
			} else {
				os.Chmod(cleanPath, os.FileMode(mode))
			}
		}
	}

	resp.Success = true
	log.Printf("[Agent] File written: %s", cleanPath)
	a.sendFileUploadResp(reqID, resp)
}

func (a *Agent) handleFileDownload(reqID string, req protocol.FileDownload) {
	log.Printf("[Agent] File download: %s", req.Path)

	resp := protocol.FileDownloadResp{AgentID: a.ID, Path: req.Path}

	// Sanitize path: reject absolute paths outside /tmp and path traversal
	cleanPath := filepath.Clean(req.Path)
	if !strings.HasPrefix(cleanPath, "/tmp/") && !strings.HasPrefix(cleanPath, "/opt/multi-ops/") {
		resp.Error = "path must be under /tmp/ or /opt/multi-ops/"
		a.sendDownloadResp(reqID, resp)
		return
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Content = string(data)
		resp.Size = int64(len(data))
		resp.Success = true
	}

	a.sendDownloadResp(reqID, resp)
}

func (a *Agent) handleCommand(cmd protocol.Command) {
	log.Printf("[Agent] Command: %s", cmd.Name)

	switch cmd.Name {
	case "restart":
		log.Println("[Agent] Restarting by remote command...")
		go func() {
			time.Sleep(1 * time.Second)
			os.Exit(0)
		}()

	case "shutdown":
		log.Println("[Agent] Shutting down by remote command...")
		close(a.stopCh)

	case "reconnect":
		if a.conn != nil {
			a.conn.Close()
		}
	}
}

func (a *Agent) sendFileUploadResp(reqID string, resp protocol.FileUploadResp) {
	msg, _ := protocol.NewMessage(protocol.TypeFileUploadResp, a.ID, resp)
	msg.ReqID = reqID
	a.send(msg)
}

func (a *Agent) sendDownloadResp(reqID string, resp protocol.FileDownloadResp) {
	msg, _ := protocol.NewMessage(protocol.TypeFileDownloadResp, a.ID, resp)
	msg.ReqID = reqID
	a.send(msg)
}

func (a *Agent) send(msg protocol.Message) {
	a.connMu.Lock()
	defer a.connMu.Unlock()

	if a.conn == nil {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[Agent] Failed to marshal message: %v", err)
		return
	}

	if err := a.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("[Agent] Failed to send message: %v", err)
	}
}

func generateID() string {
	hostname, _ := os.Hostname()
	return fmt.Sprintf("%s-%d", hostname, rand.Intn(10000))
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
