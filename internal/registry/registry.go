// Package registry 实现模式注册中心：主题版本管理、并发控制、兼容性与引用校验。
package registry

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shikejing/schema-registry-go/internal/auth"
	"github.com/shikejing/schema-registry-go/internal/schema"
	"github.com/shikejing/schema-registry-go/internal/store"
)

// Registry 模式注册中心。
type Registry struct {
	mu          sync.Mutex
	store       store.Store
	auth        *auth.Authenticator
	audit       *auth.AuditLog
	topics      map[string]*topicState
	compatModes map[string]schema.Compatibility
}

type topicState struct {
	versions map[int]*store.RegisteredSchema
	latest   int
}

// New 创建注册中心并从持久化中恢复状态（版本、兼容性配置、审计记录）。
func New(st store.Store, authenticator *auth.Authenticator, auditLog *auth.AuditLog) (*Registry, error) {
	snap, err := st.Load()
	if err != nil {
		return nil, fmt.Errorf("加载持久化状态失败: %w", err)
	}
	r := &Registry{
		store:       st,
		auth:        authenticator,
		audit:       auditLog,
		topics:      map[string]*topicState{},
		compatModes: map[string]schema.Compatibility{},
	}
	for name, ts := range snap.Topics {
		t := &topicState{versions: map[int]*store.RegisteredSchema{}}
		for v, rs := range ts.Versions {
			t.versions[v] = rs
			if v > t.latest {
				t.latest = v
			}
		}
		r.topics[name] = t
	}
	for k, v := range snap.CompatModes {
		r.compatModes[k] = v
	}
	for _, e := range snap.Audit {
		auditLog.Append(e)
	}
	return r, nil
}

// RegisterInput 注册请求。
type RegisterInput struct {
	Topic  string
	Schema *schema.Schema
	Caller string
}

// Register 注册一个新模式版本。
// 流程：结构校验 -> 引用/循环校验 -> 兼容性校验 -> 分配版本 -> 持久化 -> 提交内存 -> 审计。
// 任何校验失败都不会改动已存在的版本；持久化成功后才提交内存状态，失败不留半成品。
func (r *Registry) Register(in RegisterInput) (*store.RegisteredSchema, error) {
	if in.Topic == "" {
		return nil, errors.New("主题不能为空")
	}
	if in.Schema == nil {
		return nil, errors.New("模式不能为空")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. 结构合法性
	if errs := schema.ValidateStructure(in.Schema); len(errs) > 0 {
		return nil, fmt.Errorf("模式结构非法: %w", errors.Join(errs...))
	}
	// 2. 引用存在性与循环引用校验
	if err := r.detectCyclesLocked(in.Schema, in.Topic); err != nil {
		return nil, err
	}
	// 3. 与最新版本的兼容性校验
	if t := r.topics[in.Topic]; t != nil && t.latest > 0 {
		mode := r.modeLocked(in.Topic)
		cr := schema.CheckCompatibility(in.Schema, t.versions[t.latest].Schema, mode, r.resolverLocked())
		if !cr.Compatible {
			return nil, fmt.Errorf("兼容性校验失败（%s），注册被拒绝: %s", mode, strings.Join(cr.Errors, "; "))
		}
	}
	// 4. 分配版本号（互斥锁内完成，保证不重复、不空洞）
	t := r.topics[in.Topic]
	newVersion := 1
	if t != nil {
		newVersion = t.latest + 1
	}
	rs := &store.RegisteredSchema{
		Topic:     in.Topic,
		Version:   newVersion,
		Schema:    in.Schema,
		CreatedAt: time.Now(),
		CreatedBy: in.Caller,
	}
	// 5. 构造包含新版本与审计条目的完整快照，原子持久化
	entry := auth.AuditEntry{
		Caller: in.Caller, Action: "register", Topic: in.Topic, Version: newVersion,
		Detail: fmt.Sprintf("注册版本 %d", newVersion),
	}
	snap := r.snapshotWithLocked(rs)
	snap.Audit = append(snap.Audit, entry)
	if err := r.store.Save(snap); err != nil {
		return nil, fmt.Errorf("持久化失败，注册未生效: %w", err)
	}
	// 6. 持久化成功后才提交内存状态与审计
	r.audit.Append(entry)
	if t == nil {
		t = &topicState{versions: map[int]*store.RegisteredSchema{}}
		r.topics[in.Topic] = t
	}
	t.versions[newVersion] = rs
	t.latest = newVersion
	return rs, nil
}

// Get 读取指定版本；version <= 0 表示最新版本。
func (r *Registry) Get(topic string, version int) (*store.RegisteredSchema, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getLocked(topic, version)
}

func (r *Registry) getLocked(topic string, version int) (*store.RegisteredSchema, error) {
	t, ok := r.topics[topic]
	if !ok {
		return nil, fmt.Errorf("主题 %q 不存在", topic)
	}
	if version <= 0 {
		version = t.latest
	}
	rs, ok := t.versions[version]
	if !ok {
		return nil, fmt.Errorf("版本 %q:%d 不存在", topic, version)
	}
	return rs, nil
}

// Delete 删除指定版本；若仍被其他模式引用则拒绝。
func (r *Registry) Delete(topic string, version int, caller string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.topics[topic]
	if !ok {
		return fmt.Errorf("主题 %q 不存在", topic)
	}
	if _, ok := t.versions[version]; !ok {
		return fmt.Errorf("版本 %q:%d 不存在", topic, version)
	}
	if referenced, by := r.isReferencedLocked(topic, version); referenced {
		return fmt.Errorf("版本 %q:%d 仍被 %s 引用，拒绝删除", topic, version, by)
	}
	// 构造删除后的快照并附带审计条目，原子持久化
	entry := auth.AuditEntry{
		Caller: caller, Action: "delete", Topic: topic, Version: version,
		Detail: fmt.Sprintf("删除版本 %d", version),
	}
	snap := r.snapshotWithoutLocked(topic, version)
	snap.Audit = append(snap.Audit, entry)
	if err := r.store.Save(snap); err != nil {
		return fmt.Errorf("持久化失败，删除未生效: %w", err)
	}
	// 持久化成功后才提交内存状态
	r.audit.Append(entry)
	delete(t.versions, version)
	if t.latest == version {
		t.latest = 0
		for v := range t.versions {
			if v > t.latest {
				t.latest = v
			}
		}
	}
	return nil
}

// Validate 按指定版本的模式校验实例数据。
func (r *Registry) Validate(topic string, version int, instance any) (*schema.ValidateResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rs, err := r.getLocked(topic, version)
	if err != nil {
		return nil, err
	}
	return rs.Schema.Validate(instance, r.resolverLocked()), nil
}

// CheckCompatibility 对待注册模式与最新版本做兼容性判定（不写入）。
func (r *Registry) CheckCompatibility(topic string, s *schema.Schema) (*schema.CompatResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.topics[topic]
	if t == nil || t.latest == 0 {
		return &schema.CompatResult{Compatible: true}, nil
	}
	return schema.CheckCompatibility(s, t.versions[t.latest].Schema, r.modeLocked(topic), r.resolverLocked()), nil
}

// SetCompatibility 设置主题的兼容性级别并持久化。
func (r *Registry) SetCompatibility(topic string, mode schema.Compatibility, caller string) error {
	switch mode {
	case schema.CompatibilityNone, schema.CompatibilityBackward, schema.CompatibilityForward, schema.CompatibilityFull:
	default:
		return fmt.Errorf("非法的兼容性级别 %q", mode)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// 允许在主题创建前预配置兼容性级别；配置随快照原子持久化。
	entry := auth.AuditEntry{
		Caller: caller, Action: "set-compatibility", Topic: topic,
		Detail: fmt.Sprintf("兼容性级别设置为 %s", mode),
	}
	snap := r.snapshotLocked()
	snap.CompatModes[topic] = mode
	snap.Audit = append(snap.Audit, entry)
	if err := r.store.Save(snap); err != nil {
		return fmt.Errorf("持久化失败: %w", err)
	}
	// 持久化成功后才提交内存状态
	r.compatModes[topic] = mode
	r.audit.Append(entry)
	return nil
}

// GetCompatibility 返回主题的兼容性级别，缺省为 BACKWARD。
func (r *Registry) GetCompatibility(topic string) schema.Compatibility {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.modeLocked(topic)
}

func (r *Registry) modeLocked(topic string) schema.Compatibility {
	if m, ok := r.compatModes[topic]; ok && m != "" {
		return m
	}
	return schema.CompatibilityBackward
}

// Topics 列出全部主题（排序）。
func (r *Registry) Topics() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.topics))
	for name := range r.topics {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Versions 列出主题的全部版本号（升序）。
func (r *Registry) Versions(topic string) ([]int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.topics[topic]
	if !ok {
		return nil, fmt.Errorf("主题 %q 不存在", topic)
	}
	out := make([]int, 0, len(t.versions))
	for v := range t.versions {
		out = append(out, v)
	}
	sort.Ints(out)
	return out, nil
}

// AuditEntries 返回审计记录。
func (r *Registry) AuditEntries() []auth.AuditEntry {
	return r.audit.Entries()
}

// ---------- 内部方法（调用时必须持有 r.mu） ----------

// resolverLocked 返回引用解析器（调用时持有锁）。
func (r *Registry) resolverLocked() schema.Resolver {
	return func(ref string) (*schema.Schema, error) {
		rs, err := r.resolveRefLocked(ref)
		if err != nil {
			return nil, err
		}
		return rs.Schema, nil
	}
}

// resolveRefLocked 解析 "topic" 或 "topic:version" 形式的引用。
func (r *Registry) resolveRefLocked(ref string) (*store.RegisteredSchema, error) {
	topic, version, err := schema.ParseRef(ref)
	if err != nil {
		return nil, err
	}
	t, ok := r.topics[topic]
	if !ok {
		return nil, fmt.Errorf("引用的主题 %q 不存在", topic)
	}
	if version == 0 {
		version = t.latest
	}
	rs, ok := t.versions[version]
	if !ok {
		return nil, fmt.Errorf("引用的版本 %q 不存在", ref)
	}
	return rs, nil
}

// detectCyclesLocked 通过 DFS 检测引用环。
// 任何指向当前注册主题的无版本引用（"topic"）在提交后都会指向新版本，构成自环，直接拒绝。
func (r *Registry) detectCyclesLocked(s *schema.Schema, topic string) error {
	path := map[string]bool{}
	var dfs func(sch *schema.Schema) error
	dfs = func(sch *schema.Schema) error {
		for _, ref := range collectRefs(sch) {
			refTopic, refVersion, err := schema.ParseRef(ref)
			if err != nil {
				return err
			}
			if refTopic == topic && refVersion == 0 {
				return fmt.Errorf("模式引用了自身主题 %q 的最新版本，构成循环引用", topic)
			}
			target, err := r.resolveRefLocked(ref)
			if err != nil {
				return err
			}
			node := fmt.Sprintf("%s:%d", target.Topic, target.Version)
			if path[node] {
				return fmt.Errorf("检测到循环引用，环节点为 %s", node)
			}
			path[node] = true
			if err := dfs(target.Schema); err != nil {
				return err
			}
			delete(path, node)
		}
		return nil
	}
	return dfs(s)
}

// isReferencedLocked 检查指定版本是否仍被其他模式引用。
func (r *Registry) isReferencedLocked(topic string, version int) (bool, string) {
	for tName, t := range r.topics {
		for v, rs := range t.versions {
			if tName == topic && v == version {
				continue
			}
			for _, ref := range collectRefs(rs.Schema) {
				rt, rv, err := schema.ParseRef(ref)
				if err != nil {
					continue
				}
				if rt != topic {
					continue
				}
				if rv == version {
					return true, fmt.Sprintf("%s:%d", tName, v)
				}
				if rv == 0 && r.topics[topic].latest == version {
					return true, fmt.Sprintf("%s:%d", tName, v)
				}
			}
		}
	}
	return false, ""
}

// collectRefs 收集模式中出现的全部引用。
func collectRefs(s *schema.Schema) []string {
	var refs []string
	var walkField func(f schema.Field)
	var walkFields func(fields []schema.Field)
	walkField = func(f schema.Field) {
		if f.Ref != "" {
			refs = append(refs, f.Ref)
		}
		if f.Items != nil {
			walkField(*f.Items)
		}
		walkFields(f.Fields)
	}
	walkFields = func(fields []schema.Field) {
		for _, f := range fields {
			walkField(f)
		}
	}
	if s.Ref != "" {
		refs = append(refs, s.Ref)
	}
	walkFields(s.Fields)
	if s.Items != nil {
		walkField(*s.Items)
	}
	return refs
}

// snapshotLocked 构造当前完整快照。
func (r *Registry) snapshotLocked() *store.Snapshot {
	snap := &store.Snapshot{
		Topics:      map[string]*store.TopicSnapshot{},
		CompatModes: map[string]schema.Compatibility{},
	}
	for name, t := range r.topics {
		ts := &store.TopicSnapshot{Versions: map[int]*store.RegisteredSchema{}}
		for v, rs := range t.versions {
			ts.Versions[v] = rs
		}
		snap.Topics[name] = ts
	}
	for k, v := range r.compatModes {
		snap.CompatModes[k] = v
	}
	snap.Audit = r.audit.Entries()
	return snap
}

// snapshotWithLocked 构造包含新版本的快照（不修改内存状态）。
func (r *Registry) snapshotWithLocked(rs *store.RegisteredSchema) *store.Snapshot {
	snap := r.snapshotLocked()
	ts := snap.Topics[rs.Topic]
	if ts == nil {
		ts = &store.TopicSnapshot{Versions: map[int]*store.RegisteredSchema{}}
		snap.Topics[rs.Topic] = ts
	}
	ts.Versions[rs.Version] = rs
	return snap
}

// snapshotWithoutLocked 构造删除指定版本后的快照（不修改内存状态）。
func (r *Registry) snapshotWithoutLocked(topic string, version int) *store.Snapshot {
	snap := r.snapshotLocked()
	if ts := snap.Topics[topic]; ts != nil {
		delete(ts.Versions, version)
	}
	return snap
}
