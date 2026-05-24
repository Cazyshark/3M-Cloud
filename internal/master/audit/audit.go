package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
	User      string    `json:"user,omitempty"`
	IP        string    `json:"ip,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}

type Logger struct {
	file   *os.File
	mu     sync.Mutex
	entries []Entry
	maxSize int
}

func NewLogger(logDir string) (*Logger, error) {
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(filepath.Join(logDir, "audit.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}

	return &Logger{
		file:    f,
		entries: make([]Entry, 0, 1000),
		maxSize: 1000,
	}, nil
}

func (l *Logger) Log(entry Entry) {
	if l == nil {
		return
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = append(l.entries, entry)
	if len(l.entries) > l.maxSize {
		l.entries = l.entries[len(l.entries)-l.maxSize:]
	}

	data, _ := json.Marshal(entry)
	l.file.Write(append(data, '\n'))
}

func (l *Logger) Recent(n int) []Entry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if n > len(l.entries) {
		n = len(l.entries)
	}
	result := make([]Entry, n)
	copy(result, l.entries[len(l.entries)-n:])
	return result
}

func (l *Logger) Close() error {
	return l.file.Close()
}
