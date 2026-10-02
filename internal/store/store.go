// Package store 提供注册中心状态的持久化。
package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/shikejing/schema-registry-go/internal/auth"
	"github.com/shikejing/schema-registry-go/internal/schema"
)

// RegisteredSchema 是已提交的一个模式版本。
type RegisteredSchema struct {
	Topic     string         `json:"topic"`
	Version   int            `json:"version"`
	Schema    *schema.Schema `json:"schema"`
	CreatedAt time.Time      `json:"createdAt"`
	CreatedBy string         `json:"createdBy"`
}

// Snapshot 是注册中心某一时刻的完整状态。
type Snapshot struct {
	Topics      map[string]*TopicSnapshot       `json:"topics"`
	CompatModes map[string]schema.Compatibility `json:"compatModes,omitempty"`
	Audit       []auth.AuditEntry               `json:"audit,omitempty"`
}

// TopicSnapshot 是一个主题的全部版本。
type TopicSnapshot struct {
	Versions map[int]*RegisteredSchema `json:"versions"`
}

// Store 持久化接口。
type Store interface {
	// Save 原子地写入完整快照：先写临时文件再 rename，
	// 保证任何时刻崩溃都不会留下半成品状态。
	Save(snap *Snapshot) error
	// Load 读取快照；无数据时返回空快照。
	Load() (*Snapshot, error)
}

// FileStore 基于文件的持久化实现。
type FileStore struct {
	path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) Save(snap *Snapshot) error {
	if snap == nil {
		return errors.New("store: 快照不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// rename 是原子操作，崩溃后要么是旧完整文件，要么是新完整文件。
	return os.Rename(tmp, s.path)
}

func (s *FileStore) Load() (*Snapshot, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Snapshot{Topics: map[string]*TopicSnapshot{}}, nil
		}
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	if snap.Topics == nil {
		snap.Topics = map[string]*TopicSnapshot{}
	}
	if snap.CompatModes == nil {
		snap.CompatModes = map[string]schema.Compatibility{}
	}
	return &snap, nil
}
