// Command schema-registry 启动面向低代码表单的模式注册中心 HTTP 服务。
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/shikejing/schema-registry-go/internal/auth"
	"github.com/shikejing/schema-registry-go/internal/registry"
	"github.com/shikejing/schema-registry-go/internal/schema"
	"github.com/shikejing/schema-registry-go/internal/store"
)

func main() {
	dataPath := envOr("DATA_PATH", "data/registry.json")
	addr := envOr("ADDR", ":8080")

	st := store.NewFileStore(dataPath)
	auditLog := auth.NewAuditLog()
	authenticator := auth.NewAuthenticator(auditLog)
	// 默认账户：admin/admin123, writer/writer123, reader/reader123
	authenticator.AddKey("admin123", auth.RoleAdmin)
	authenticator.AddKey("writer123", auth.RoleWriter)
	authenticator.AddKey("reader123", auth.RoleReader)

	reg, err := registry.New(st, authenticator, auditLog)
	if err != nil {
		log.Fatalf("初始化注册中心失败: %v", err)
	}

	mux := http.NewServeMux()
	registerRoutes(mux, reg, authenticator, auditLog)

	log.Printf("模式注册中心监听 %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ---------- HTTP 层 ----------

type api struct {
	reg      *registry.Registry
	auth     *auth.Authenticator
	auditLog *auth.AuditLog
}

func registerRoutes(mux *http.ServeMux, reg *registry.Registry, authenticator *auth.Authenticator, auditLog *auth.AuditLog) {
	a := &api{reg: reg, auth: authenticator, auditLog: auditLog}

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// 主题与版本
	mux.HandleFunc("/topics", a.withAuth(auth.ActionRead, a.handleTopics))
	mux.HandleFunc("/topics/", a.withAuth(auth.ActionRead, a.handleTopicItem))
	mux.HandleFunc("/audit", a.withAuth(auth.ActionAdmin, a.handleAudit))
}

// withAuth 鉴权中间件：校验 X-API-Key 并按操作类型授权。
func (a *api) withAuth(action string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		role, ok := a.auth.Authenticate(key)
		if !ok {
			writeError(w, http.StatusUnauthorized, "无效的 API Key")
			return
		}
		if !auth.Authorize(role, action) {
			writeError(w, http.StatusForbidden, "当前角色无权执行该操作")
			return
		}
		next(w, r)
	}
}

func (a *api) handleTopics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"topics": a.reg.Topics()})
}

// handleTopicItem 处理 /topics/{topic} 与 /topics/{topic}/... 路径。
func (a *api) handleTopicItem(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/topics/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "缺少主题名")
		return
	}
	topic := parts[0]
	rest := parts[1:]

	switch {
	case len(rest) == 0:
		// GET /topics/{topic} -> 主题信息（版本列表 + 兼容性配置）
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "方法不允许")
			return
		}
		versions, err := a.reg.Versions(topic)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"topic":         topic,
			"versions":      versions,
			"compatibility": a.reg.GetCompatibility(topic),
		})
	case rest[0] == "versions" && len(rest) == 1:
		a.handleVersions(w, r, topic)
	case rest[0] == "versions" && len(rest) == 2:
		a.handleVersionItem(w, r, topic, rest[1])
	case rest[0] == "validate" && len(rest) == 1:
		a.handleValidate(w, r, topic)
	case rest[0] == "compatibility" && len(rest) == 1:
		a.handleCompatibility(w, r, topic)
	default:
		writeError(w, http.StatusNotFound, "路径不存在")
	}
}

// handleVersions 处理 /topics/{topic}/versions。
func (a *api) handleVersions(w http.ResponseWriter, r *http.Request, topic string) {
	switch r.Method {
	case http.MethodGet:
		versions, err := a.reg.Versions(topic)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"topic": topic, "versions": versions})
	case http.MethodPost:
		// 写操作需要 writer 及以上角色
		if !a.authorizeWriter(w, r) {
			return
		}
		var s schema.Schema
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
			return
		}
		rs, err := a.reg.Register(registry.RegisterInput{
			Topic: topic, Schema: &s, Caller: r.Header.Get("X-API-Key"),
		})
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, rs)
	default:
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

// handleVersionItem 处理 /topics/{topic}/versions/{version}。
func (a *api) handleVersionItem(w http.ResponseWriter, r *http.Request, topic, verStr string) {
	version := 0
	if verStr != "latest" {
		v, err := strconv.Atoi(verStr)
		if err != nil || v <= 0 {
			writeError(w, http.StatusBadRequest, "版本号必须为正整数或 latest")
			return
		}
		version = v
	}
	switch r.Method {
	case http.MethodGet:
		rs, err := a.reg.Get(topic, version)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rs)
	case http.MethodDelete:
		// 删除操作仅 admin
		if !a.authorizeRole(w, r, auth.RoleAdmin, auth.ActionDelete) {
			return
		}
		if err := a.reg.Delete(topic, version, r.Header.Get("X-API-Key")); err != nil {
			if strings.Contains(err.Error(), "仍被") {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "topic": topic, "version": version})
	default:
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

// handleValidate 处理 /topics/{topic}/validate?version=X。
func (a *api) handleValidate(w http.ResponseWriter, r *http.Request, topic string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	version := 0
	if v := r.URL.Query().Get("version"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "version 必须为正整数")
			return
		}
		version = n
	}
	var instance any
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&instance); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	result, err := a.reg.Validate(topic, version, instance)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleCompatibility 处理 /topics/{topic}/compatibility。
func (a *api) handleCompatibility(w http.ResponseWriter, r *http.Request, topic string) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"topic":         topic,
			"compatibility": a.reg.GetCompatibility(topic),
		})
	case http.MethodPost:
		// 对待注册模式做兼容性预校验（writer 及以上）
		if !a.authorizeWriter(w, r) {
			return
		}
		var s schema.Schema
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
			return
		}
		result, err := a.reg.CheckCompatibility(topic, &s)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPut:
		// 设置兼容性级别（仅 admin）
		if !a.authorizeRole(w, r, auth.RoleAdmin, auth.ActionAdmin) {
			return
		}
		var body struct {
			Compatibility schema.Compatibility `json:"compatibility"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
			return
		}
		if err := a.reg.SetCompatibility(topic, body.Compatibility, r.Header.Get("X-API-Key")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"topic": topic, "compatibility": body.Compatibility})
	default:
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
	}
}

func (a *api) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": a.auditLog.Entries()})
}

// authorizeWriter 在 withAuth 已通过读权限的基础上，要求 writer 及以上角色。
func (a *api) authorizeWriter(w http.ResponseWriter, r *http.Request) bool {
	return a.authorizeRole(w, r, auth.RoleWriter, auth.ActionWrite)
}

func (a *api) authorizeRole(w http.ResponseWriter, r *http.Request, minRole auth.Role, action string) bool {
	role, ok := a.auth.Authenticate(r.Header.Get("X-API-Key"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "无效的 API Key")
		return false
	}
	if !auth.Authorize(role, action) {
		writeError(w, http.StatusForbidden, "当前角色无权执行该操作")
		return false
	}
	_ = minRole
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
