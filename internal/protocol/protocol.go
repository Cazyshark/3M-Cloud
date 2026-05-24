package protocol

import "encoding/json"

// Message types
const (
	TypeRegister       = "register"
	TypeHeartbeat      = "heartbeat"
	TypeMetrics        = "metrics"
	TypeTerminalInput  = "terminal_input"
	TypeTerminalOutput = "terminal_output"
	TypeTerminalResize = "terminal_resize"
	TypeExecRequest    = "exec_request"
	TypeExecResponse   = "exec_response"
	TypeExecStream     = "exec_stream"
	TypeMachineInfo    = "machine_info"
	TypeCommand        = "command"
	TypeFileUpload     = "file_upload"
	TypeFileUploadResp = "file_upload_resp"
	TypeFileDownload   = "file_download"
	TypeFileDownloadResp = "file_download_resp"
)

// Message is the universal envelope for all communication between agent/gateway/master
type Message struct {
	Type    string          `json:"type"`
	AgentID string          `json:"agent_id,omitempty"`
	ReqID   string          `json:"req_id,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func NewMessage(msgType, agentID string, data interface{}) (Message, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Message{}, err
	}
	return Message{
		Type:    msgType,
		AgentID: agentID,
		Data:    raw,
	}, nil
}

func (m Message) Decode(v interface{}) error {
	return json.Unmarshal(m.Data, v)
}

// AgentInfo holds machine registration and status data
type AgentInfo struct {
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	// LSB info
	DistributorID string `json:"distributor_id"`
	Description   string `json:"description"`
	Release       string `json:"release"`
	Codename      string `json:"codename"`
	// Network
	PublicIP string `json:"public_ip"`
	Location string `json:"location"`
	// Hardware
	CPUModel  string `json:"cpu_model"`
	CPUCores  int    `json:"cpu_cores"`
	MemoryMB  uint64 `json:"memory_mb"`
	DiskGB    uint64 `json:"disk_gb"`
	DiskUsed  uint64 `json:"disk_used"`
	// Runtime
	Uptime   uint64 `json:"uptime"`
	Status   string `json:"status"` // online, offline
	LastSeen int64  `json:"last_seen"`
	// Tags (managed by master, not agent)
	Tags []string `json:"tags,omitempty"`
	// Group
	Group string `json:"group,omitempty"`
	// Real-time metrics
	Metrics *MachineMetrics `json:"metrics,omitempty"`
}

// MachineMetrics holds real-time performance data pushed periodically by agent
type MachineMetrics struct {
	CPUPercent   float64 `json:"cpu_percent"`
	MemPercent   float64 `json:"mem_percent"`
	MemUsedMB    uint64  `json:"mem_used_mb"`
	DiskPercent  float64 `json:"disk_percent"`
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
	NetRxBytes   uint64  `json:"net_rx_bytes"`
	NetTxBytes   uint64  `json:"net_tx_bytes"`
	TCPConns     int     `json:"tcp_conns"`
	ProcessCount int     `json:"process_count"`
	Timestamp    int64   `json:"timestamp"`
}

// TerminalInput is sent from browser → master → gateway → agent
type TerminalInput struct {
	SessionID string `json:"session_id"`
	Data      string `json:"data"`
}

// TerminalOutput is sent from agent → gateway → master → browser
type TerminalOutput struct {
	SessionID string `json:"session_id"`
	Data      string `json:"data"`
}

// TerminalResize is sent from browser to agent to resize pty
type TerminalResize struct {
	SessionID string `json:"session_id"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

// ExecRequest is a request to execute a script
type ExecRequest struct {
	Script   string   `json:"script"`
	Timeout  int      `json:"timeout"` // seconds, 0 = no timeout
	AgentIDs []string `json:"agent_ids,omitempty"`
}

// ExecResponse is the result of script execution
type ExecResponse struct {
	AgentID  string `json:"agent_id"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

// ExecStream is a streaming chunk of script output
type ExecStream struct {
	AgentID string `json:"agent_id"`
	Chunk   string `json:"chunk"`
	Done    bool   `json:"done"`
}

// Command is a generic command from master to agent
type Command struct {
	Name string            `json:"name"`
	Args map[string]string `json:"args,omitempty"`
}

// FileUpload sends a file to an agent
type FileUpload struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Mode      string `json:"mode"`
	Overwrite bool   `json:"overwrite"`
}

// FileUploadResp is the response from agent
type FileUploadResp struct {
	AgentID string `json:"agent_id"`
	Path    string `json:"path"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// FileDownload requests a file from agent
type FileDownload struct {
	Path string `json:"path"`
}

// FileDownloadResp is the response from agent
type FileDownloadResp struct {
	AgentID string `json:"agent_id"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Size    int64  `json:"size"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}
