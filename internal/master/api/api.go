package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multi-ops/internal/master/audit"
	"github.com/multi-ops/internal/master/auth"
	"github.com/multi-ops/internal/master/middleware"
	"github.com/multi-ops/internal/master/store"
	"github.com/multi-ops/internal/master/ws"
	"github.com/multi-ops/internal/protocol"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false
		}
		host := r.Host
		// Accept same-origin only
		return origin == "http://"+host || origin == "https://"+host ||
			strings.HasPrefix(origin, "https://"+host) || strings.HasPrefix(origin, "http://"+host)
	},
}

type Server struct {
	Store      *store.Store
	WSHub      *ws.Hub
	Gateway    *ws.GatewayConn
	History    *store.ExecHistory
	Users      *auth.UserStore
	JWT        *auth.JWTManager
	AuditLog   *audit.Logger
}

func New(s *store.Store, hub *ws.Hub, gw *ws.GatewayConn, h *store.ExecHistory, u *auth.UserStore, jwt *auth.JWTManager, al *audit.Logger) *Server {
	return &Server{
		Store:    s,
		WSHub:    hub,
		Gateway:  gw,
		History:  h,
		Users:    u,
		JWT:      jwt,
		AuditLog: al,
	}
}

// ========== Auth Handlers ==========

func (s *Server) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if !s.Users.ValidatePassword(req.Username, req.Password) {
		s.AuditLog.Log(audit.Entry{Action: "login_failed", User: req.Username, IP: middleware.ExtractIP(r)})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	user, _ := s.Users.GetUser(req.Username)
	if user != nil && user.TOTPSecret != "" {
		if !auth.ValidateCode(user.TOTPSecret, req.TOTPCode) {
			s.AuditLog.Log(audit.Entry{Action: "totp_failed", User: req.Username, IP: middleware.ExtractIP(r)})
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid TOTP code"})
			return
		}
	}

	token, err := s.JWT.GenerateToken(req.Username, user.Role)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}

	s.AuditLog.Log(audit.Entry{Action: "login_success", User: req.Username, IP: middleware.ExtractIP(r)})
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *Server) HandleSetupTOTP(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	secret, url := auth.GenerateSecret("Multi-Ops", claims.Username)
	s.Users.UpdateTOTPSecret(claims.Username, secret)

	writeJSON(w, http.StatusOK, map[string]string{
		"secret":    secret,
		"otpauth_url": url,
	})
}

// ========== Machine Handlers ==========

func (s *Server) HandleMachines(w http.ResponseWriter, r *http.Request) {
	group := r.URL.Query().Get("group")
	tag := r.URL.Query().Get("tag")

	var machines []protocol.AgentInfo
	if tag != "" {
		machines = s.Store.GetMachinesByTag(tag)
	} else if group != "" {
		machines = s.Store.GetMachinesByGroup(group)
	} else {
		machines = s.Store.GetAllMachines()
	}
	writeJSON(w, http.StatusOK, machines)
}

func (s *Server) HandleMachineDetail(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	m, ok := s.Store.GetMachine(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "machine not found"})
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) HandleMachineTags(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}

	if r.Method == "PUT" {
		var req struct {
			Tags []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		s.Store.SetTags(id, req.Tags)
		claims := middleware.ClaimsFromContext(r.Context())
		s.AuditLog.Log(audit.Entry{Action: "set_tags", User: claims.Username, AgentID: id})
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// GET — return all available tags
	writeJSON(w, http.StatusOK, s.Store.GetAllTags())
}

// HandleMachineTagsList returns all available tags (read-only, no id needed)
func (s *Server) HandleMachineTagsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Store.GetAllTags())
}

func (s *Server) HandleMachineGroup(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	group := r.URL.Query().Get("group")
	if id == "" || group == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id and group required"})
		return
	}
	s.Store.SetGroup(id, group)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) HandleGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Store.GetAllGroups())
}

// ========== Exec Handlers ==========

func (s *Server) HandleBatchExec(w http.ResponseWriter, r *http.Request) {
	var req protocol.ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if req.Script == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "script required"})
		return
	}

	// Support group-based targeting
	group := r.URL.Query().Get("group")
	if group != "" && len(req.AgentIDs) == 0 {
		groupMachines := s.Store.GetMachinesByGroup(group)
		for _, m := range groupMachines {
			if m.Status == "online" {
				req.AgentIDs = append(req.AgentIDs, m.AgentID)
			}
		}
	}

	if len(req.AgentIDs) == 0 {
		online := s.Store.GetOnlineMachines()
		for _, m := range online {
			req.AgentIDs = append(req.AgentIDs, m.AgentID)
		}
	}

	reqID := uuid.New().String()
	claims := middleware.ClaimsFromContext(r.Context())
	username := ""
	if claims != nil {
		username = claims.Username
	}

	// Record in history
	record := store.ExecRecord{
		ID:        reqID,
		Script:    req.Script,
		Timeout:   req.Timeout,
		AgentIDs:  req.AgentIDs,
		Results:   make(map[string]store.ExecResult),
		Status:    "running",
		StartedAt: time.Now(),
		User:      username,
	}
	s.History.Add(record)

	msg, _ := protocol.NewMessage(protocol.TypeExecRequest, "", req)
	msg.ReqID = reqID

	for _, agentID := range req.AgentIDs {
		singleMsg := msg
		singleMsg.AgentID = agentID
		s.Gateway.Send(singleMsg)
	}

	s.AuditLog.Log(audit.Entry{Action: "batch_exec", User: username, Detail: req.Script[:min(100, len(req.Script))]})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"request_id": reqID,
		"agent_ids":  req.AgentIDs,
		"count":      len(req.AgentIDs),
	})
}

func (s *Server) HandleExecHistory(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := parseInt(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := parseInt(o); err == nil && v >= 0 {
			offset = v
		}
	}
	records := s.History.List(limit, offset)
	// Clear full script content in list response — only keep truncated preview
	for i := range records {
		records[i].Script = records[i].ScriptBrief
		records[i].ScriptBrief = ""
	}
	writeJSON(w, http.StatusOK, records)
}

func (s *Server) HandleExecDetail(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	record, ok := s.History.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, record)
}

// ========== File Upload ==========

type fileUploadRequest struct {
	AgentIDs  []string `json:"agent_ids"`
	Path      string   `json:"path"`
	Content   string   `json:"content"`
	Mode      string   `json:"mode"`
	Overwrite bool     `json:"overwrite"`
}

func (s *Server) HandleFileUpload(w http.ResponseWriter, r *http.Request) {
	var req fileUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.Path == "" || req.Content == "" || len(req.AgentIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path, content, and agent_ids required"})
		return
	}

	reqID := uuid.New().String()
	fileMsg := protocol.FileUpload{
		Path:      req.Path,
		Content:   req.Content,
		Mode:      req.Mode,
		Overwrite: req.Overwrite,
	}
	msg, _ := protocol.NewMessage(protocol.TypeFileUpload, "", fileMsg)
	msg.ReqID = reqID

	for _, agentID := range req.AgentIDs {
		singleMsg := msg
		singleMsg.AgentID = agentID
		s.Gateway.Send(singleMsg)
	}

	claims := middleware.ClaimsFromContext(r.Context())
	s.AuditLog.Log(audit.Entry{Action: "file_upload", User: claims.Username, AgentID: stringsJoin(req.AgentIDs, ","), Detail: req.Path})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"request_id": reqID,
		"count":      len(req.AgentIDs),
	})
}

// ========== Status ==========

func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	online := s.Store.GetOnlineMachines()
	all := s.Store.GetAllMachines()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total_machines":  len(all),
		"online_machines": len(online),
		"ws_clients":      s.WSHub.ClientCount(),
	})
}

// ========== Audit ==========

func (s *Server) HandleAuditLog(w http.ResponseWriter, r *http.Request) {
	n := 100
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := parseInt(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	writeJSON(w, http.StatusOK, s.AuditLog.Recent(n))
}

// ========== WebSocket Handlers ==========

func (s *Server) HandleDashboardWS(w http.ResponseWriter, r *http.Request) {
	// Validate JWT — token required, no anonymous access
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims, err := s.JWT.ValidateToken(token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[API] Dashboard WS upgrade failed: %v", err)
		return
	}

	clientID := uuid.New().String()
	client := &ws.Client{ID: clientID, Conn: conn}
	s.WSHub.Register(client)

	defer func() {
		s.WSHub.Unregister(clientID)
		conn.Close()
	}()

	// Send current machine list on connect
	machines := s.Store.GetAllMachines()
	initMsg, _ := protocol.NewMessage("machine_list", "", machines)
	data, _ := json.Marshal(initMsg)
	conn.WriteMessage(websocket.TextMessage, data)

	// Track role for exec/terminal authorization
	isAdmin := claims.Role == "admin"

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg protocol.Message
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case protocol.TypeTerminalInput:
			s.Gateway.Send(msg)

		case protocol.TypeTerminalResize:
			s.Gateway.Send(msg)

		case protocol.TypeExecRequest:
			var req protocol.ExecRequest
			if err := msg.Decode(&req); err != nil {
				continue
			}
			reqID := uuid.New().String()
			msg.ReqID = reqID

			s.History.Add(store.ExecRecord{
				ID:        reqID,
				Script:    req.Script,
				Timeout:   req.Timeout,
				AgentIDs:  req.AgentIDs,
				Results:   make(map[string]store.ExecResult),
				Status:    "running",
				StartedAt: time.Now(),
				User:      claims.Username,
			})

			for _, agentID := range req.AgentIDs {
				singleMsg := msg
				singleMsg.AgentID = agentID
				s.Gateway.Send(singleMsg)
			}

		case protocol.TypeFileUpload:
			_ = isAdmin // viewer role can also upload (file ops are read-only for them)
			s.Gateway.Send(msg)

		case "subscribe":
			conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"subscribed"}`))
		}
	}
}

func (s *Server) HandleGatewayWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[API] Gateway WS upgrade failed: %v", err)
		return
	}

	s.Gateway.SetConn(conn)
	defer func() {
		s.Gateway.ClearConn()
		conn.Close()
	}()

	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg protocol.Message
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case protocol.TypeRegister:
			var info protocol.AgentInfo
			if err := msg.Decode(&info); err != nil {
				continue
			}
			info.AgentID = msg.AgentID
			s.Store.UpdateMachine(info)
			s.WSHub.Broadcast(map[string]interface{}{
				"type": "machine_update",
				"data": info,
			})

		case protocol.TypeTerminalOutput:
			var output protocol.TerminalOutput
			if err := msg.Decode(&output); err != nil {
				continue
			}
			s.WSHub.Broadcast(map[string]interface{}{
				"type":     "terminal_output",
				"agent_id": msg.AgentID,
				"data":     output,
			})

		case protocol.TypeExecResponse:
			var resp protocol.ExecResponse
			if err := msg.Decode(&resp); err != nil {
				continue
			}
			// Update history
			s.History.UpdateResult(msg.ReqID, store.ExecResult{
				AgentID:  msg.AgentID,
				ExitCode: resp.ExitCode,
				Output:   resp.Output,
				Error:    resp.Error,
				Done:     true,
			})
			s.WSHub.Broadcast(map[string]interface{}{
				"type":     "exec_response",
				"req_id":   msg.ReqID,
				"agent_id": msg.AgentID,
				"data":     resp,
			})

		case protocol.TypeFileUploadResp:
			var resp protocol.FileUploadResp
			if err := msg.Decode(&resp); err != nil {
				continue
			}
			s.WSHub.Broadcast(map[string]interface{}{
				"type":     "file_upload_resp",
				"req_id":   msg.ReqID,
				"agent_id": msg.AgentID,
				"data":     resp,
			})

		case protocol.TypeMetrics:
			var metrics protocol.MachineMetrics
			if err := msg.Decode(&metrics); err != nil {
				continue
			}
			s.Store.UpdateMetrics(msg.AgentID, &metrics)
			s.WSHub.Broadcast(map[string]interface{}{
				"type":     "metrics",
				"agent_id": msg.AgentID,
				"data":     metrics,
			})

		case protocol.TypeFileDownloadResp:
			var resp protocol.FileDownloadResp
			if err := msg.Decode(&resp); err != nil {
				continue
			}
			s.WSHub.Broadcast(map[string]interface{}{
				"type":     "file_download_resp",
				"req_id":   msg.ReqID,
				"agent_id": msg.AgentID,
				"data":     resp,
			})

		case protocol.TypeHeartbeat:
			s.Store.UpdateMachine(protocol.AgentInfo{
				AgentID: msg.AgentID,
				Status:  "online",
			})
		}
	}
}


// ========== File Download ==========

func (s *Server) HandleFileDownload(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	path := r.URL.Query().Get("path")
	if agentID == "" || path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id and path required"})
		return
	}

	reqID := uuid.New().String()
	msg, _ := protocol.NewMessage(protocol.TypeFileDownload, agentID, protocol.FileDownload{Path: path})
	msg.ReqID = reqID
	msg.AgentID = agentID
	s.Gateway.Send(msg)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"request_id": reqID,
		"agent_id":   agentID,
		"path":       path,
	})
}

// ========== Remote Command ==========

func (s *Server) HandleCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID string `json:"agent_id"`
		Command string `json:"command"` // restart, shutdown, reconnect
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.AgentID == "" || req.Command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id and command required"})
		return
	}

	msg, _ := protocol.NewMessage(protocol.TypeCommand, req.AgentID, protocol.Command{
		Name: req.Command,
	})
	msg.AgentID = req.AgentID
	s.Gateway.Send(msg)

	claims := middleware.ClaimsFromContext(r.Context())
	s.AuditLog.Log(audit.Entry{Action: "remote_command", User: claims.Username, AgentID: req.AgentID, Detail: req.Command})
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// ========== Script Templates ==========

type ScriptTemplate struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Script string `json:"script"`
}

var scriptTemplates = []ScriptTemplate{
	{ID: "sysinfo", Name: "系统信息", Script: "echo '=== System Info ==='\nuname -a\ncat /etc/os-release | head -5\nuptime"},
	{ID: "disk", Name: "磁盘使用", Script: "echo '=== Disk Usage ==='\ndf -h\necho '=== Inodes ==='\ndf -i"},
	{ID: "net", Name: "网络信息", Script: "echo '=== Network ==='\nip addr show | grep 'inet '\necho '=== Connections ==='\nss -tlnp | head -20"},
	{ID: "process", Name: "进程TOP", Script: "echo '=== Top CPU ==='\nps aux --sort=-%cpu | head -10\necho '=== Top Memory ==='\nps aux --sort=-%mem | head -10"},
	{ID: "security", Name: "安全检查", Script: "echo '=== Last Logins ==='\nlast -10\necho '=== Failed SSH ==='\ngrep 'Failed password' /var/log/auth.log 2>/dev/null | tail -10 || journalctl -u sshd | grep Failed | tail -10"},
	{ID: "docker", Name: "Docker状态", Script: "echo '=== Docker Containers ==='\ndocker ps -a 2>/dev/null || echo 'Docker not installed'\necho '=== Docker Images ==='\ndocker images 2>/dev/null"},
	{ID: "update", Name: "系统更新", Script: "apt list --upgradable 2>/dev/null | head -20 || yum check-update 2>/dev/null | head -20"},
	{ID: "mem_detail", Name: "内存详情", Script: "echo '=== Memory ==='\nfree -h\necho '=== Top Memory Processes ==='\nps aux --sort=-%mem | head -10"},
}

func (s *Server) HandleScriptTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, scriptTemplates)
}

// ========== Helpers ==========

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func parseInt(s string) (int, error) {
	var v int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid")
		}
		v = v*10 + int(c-'0')
	}
	return v, nil
}

func stringsJoin(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
