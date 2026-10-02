// Package auth 提供鉴权与审计。
package auth

import (
	"sort"
	"sync"
	"time"
)

// Role 调用方角色。
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleWriter Role = "writer"
	RoleReader Role = "reader"
)

// 操作分类。
const (
	ActionRead   = "read"
	ActionWrite  = "write"
	ActionDelete = "delete"
	ActionAdmin  = "admin"
)

// AuditEntry 一条审计记录。
type AuditEntry struct {
	Time    time.Time `json:"time"`
	Caller  string    `json:"caller"`
	Role    Role      `json:"role"`
	Action  string    `json:"action"`
	Topic   string    `json:"topic,omitempty"`
	Version int       `json:"version,omitempty"`
	Detail  string    `json:"detail,omitempty"`
}

// AuditLog 审计日志，线程安全。
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func NewAuditLog() *AuditLog {
	return &AuditLog{}
}

func (l *AuditLog) Append(e AuditEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	l.entries = append(l.entries, e)
}

// Entries 返回按时间排序的审计记录副本。
func (l *AuditLog) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEntry, len(l.entries))
	copy(out, l.entries)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Time.Before(out[j].Time)
	})
	return out
}

// Authenticator 基于 API Key 的鉴权器。
type Authenticator struct {
	mu    sync.RWMutex
	keys  map[string]Role
	audit *AuditLog
}

func NewAuthenticator(audit *AuditLog) *Authenticator {
	return &Authenticator{
		keys:  map[string]Role{},
		audit: audit,
	}
}

// AddKey 注册一个 API Key 及其角色。
func (a *Authenticator) AddKey(key string, role Role) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys[key] = role
}

// Authenticate 校验 key 并返回角色。
func (a *Authenticator) Authenticate(key string) (Role, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	role, ok := a.keys[key]
	return role, ok
}

// Authorize 检查角色是否允许某操作。
// 读操作：reader/writer/admin；写操作：writer/admin；删除与管理操作：仅 admin。
func Authorize(role Role, action string) bool {
	switch action {
	case ActionRead:
		return role == RoleReader || role == RoleWriter || role == RoleAdmin
	case ActionWrite:
		return role == RoleWriter || role == RoleAdmin
	case ActionDelete, ActionAdmin:
		return role == RoleAdmin
	}
	return false
}
