package store

import (
	"sync"
	"time"
)

// ExecHistory stores script execution history
type ExecHistory struct {
	records []ExecRecord
	mu      sync.Mutex
	maxSize int
}

type ExecRecord struct {
	ID           string                 `json:"id"`
	Script       string                 `json:"script"`
	ScriptBrief  string                 `json:"script_brief,omitempty"` // truncated preview
	Timeout      int                    `json:"timeout"`
	AgentIDs     []string               `json:"agent_ids"`
	Results      map[string]ExecResult  `json:"results"`
	Status       string                 `json:"status"` // running, completed, partial
	StartedAt    time.Time              `json:"started_at"`
	FinishedAt   *time.Time             `json:"finished_at,omitempty"`
	User         string                 `json:"user,omitempty"`
}

type ExecResult struct {
	AgentID  string `json:"agent_id"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
	Done     bool   `json:"done"`
}

func NewExecHistory(maxSize int) *ExecHistory {
	if maxSize <= 0 {
		maxSize = 500
	}
	return &ExecHistory{
		records: make([]ExecRecord, 0),
		maxSize: maxSize,
	}
}

func (h *ExecHistory) Add(record ExecRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Generate truncated preview (first 100 chars) for list responses
	if record.ScriptBrief == "" && len(record.Script) > 100 {
		record.ScriptBrief = record.Script[:100] + "..."
	}

	h.records = append(h.records, record)
	if len(h.records) > h.maxSize {
		h.records = h.records[len(h.records)-h.maxSize:]
	}
}

func (h *ExecHistory) UpdateResult(id string, result ExecResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.records {
		if h.records[i].ID == id {
			h.records[i].Results[result.AgentID] = result
			// Check if all done
			allDone := true
			for _, r := range h.records[i].Results {
				if !r.Done {
					allDone = false
					break
				}
			}
			if allDone {
				now := time.Now()
				h.records[i].FinishedAt = &now
				h.records[i].Status = "completed"
			}
			return
		}
	}
}

func (h *ExecHistory) Get(id string) (ExecRecord, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.ID == id {
			return r, true
		}
	}
	return ExecRecord{}, false
}

func (h *ExecHistory) List(limit, offset int) []ExecRecord {
	h.mu.Lock()
	defer h.mu.Unlock()

	total := len(h.records)
	if offset >= total {
		return nil
	}
	end := offset + limit
	if end > total {
		end = total
	}

	// Return in reverse chronological order
	result := make([]ExecRecord, 0, limit)
	for i := total - 1 - offset; i >= 0 && len(result) < limit; i-- {
		result = append(result, h.records[i])
	}
	return result
}
