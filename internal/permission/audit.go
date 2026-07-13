package permission

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

type AuditEntry struct {
	Time      time.Time          `json:"time"`
	Tool      string             `json:"tool"`
	Arguments map[string]any     `json:"arguments,omitempty"`
	Command   string             `json:"command,omitempty"`
	Decision  PermissionDecision `json:"decision"`
	Risk      string             `json:"risk"`
	Reasons   []string           `json:"reasons,omitempty"`
	User      string             `json:"user,omitempty"`
	Duration  time.Duration      `json:"duration"`
}

type AuditLogger interface {
	Log(entry AuditEntry) error
}

type NopAuditLogger struct{}

func (NopAuditLogger) Log(AuditEntry) error { return nil }

type JSONAuditLogger struct {
	w  io.Writer
	mu sync.Mutex
}

func NewJSONAuditLogger(w io.Writer) *JSONAuditLogger { return &JSONAuditLogger{w: w} }

func (l *JSONAuditLogger) Log(entry AuditEntry) error {
	if l == nil || l.w == nil {
		return fmt.Errorf("audit writer is not configured")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return json.NewEncoder(l.w).Encode(entry)
}
