// Package audit provides an append-only audit trail of registry operations.
// Every mutation attempt (allowed or denied) is recorded with the acting
// principal and a UTC timestamp, so all changes are traceable.
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Result is the authorization/outcome of an audited operation.
type Result string

const (
	ResultAllowed Result = "ALLOWED"
	ResultDenied  Result = "DENIED"
)

// Event is one immutable audit record.
type Event struct {
	Time    string `json:"time"`
	Actor   string `json:"actor"`
	Action  string `json:"action"`
	Subject string `json:"subject,omitempty"`
	Version int    `json:"version,omitempty"`
	Result  Result `json:"result"`
	Detail  string `json:"detail,omitempty"`
}

// Logger is the audit sink.
type Logger interface {
	Log(e Event) error
	Query() ([]Event, error)
	Close() error
}

// NewEvent builds an event stamped with the current UTC time.
func NewEvent(actor, action, subject string, version int, result Result, detail string) Event {
	return Event{
		Time:    time.Now().UTC().Format(time.RFC3339Nano),
		Actor:   actor,
		Action:  action,
		Subject: subject,
		Version: version,
		Result:  result,
		Detail:  detail,
	}
}

// FileLogger appends JSON-encoded events, one per line, to a file. Existing
// files are opened in append mode so the trail survives restarts.
type FileLogger struct {
	mu sync.Mutex
	f  *os.File
}

// NewFileLogger opens (creating if needed) an append-only JSONL audit log.
func NewFileLogger(path string) (*FileLogger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileLogger{f: f}, nil
}

// Log appends one event and flushes it immediately.
func (l *FileLogger) Log(e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Write(append(b, '\n')); err != nil {
		return err
	}
	return l.f.Sync()
}

// Query reads all recorded events back from the file in chronological order.
func (l *FileLogger) Query() ([]Event, error) {
	l.mu.Lock()
	path := l.f.Name()
	l.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Close releases the underlying file.
func (l *FileLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// MemoryLogger keeps events in memory; it is concurrency safe and mainly
// intended for tests.
type MemoryLogger struct {
	mu     sync.Mutex
	events []Event
}

// NewMemoryLogger returns an in-memory audit log.
func NewMemoryLogger() *MemoryLogger { return &MemoryLogger{} }

// Log records one event in memory.
func (l *MemoryLogger) Log(e Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
	return nil
}

// Query returns a copy of all recorded events.
func (l *MemoryLogger) Query() ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out, nil
}

// Close is a no-op for the memory logger.
func (l *MemoryLogger) Close() error { return nil }
