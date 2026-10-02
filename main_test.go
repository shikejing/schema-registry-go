package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shikejing/schema-registry-go/internal/auth"
	"github.com/shikejing/schema-registry-go/internal/registry"
	"github.com/shikejing/schema-registry-go/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *registry.Registry) {
	t.Helper()
	dir := t.TempDir()
	st := store.NewFileStore(dir + "/registry.json")
	auditLog := auth.NewAuditLog()
	authenticator := auth.NewAuthenticator(auditLog)
	authenticator.AddKey("admin-key", auth.RoleAdmin)
	authenticator.AddKey("writer-key", auth.RoleWriter)
	authenticator.AddKey("reader-key", auth.RoleReader)
	reg, err := registry.New(st, authenticator, auditLog)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerRoutes(mux, reg, authenticator, auditLog)
	return httptest.NewServer(mux), reg
}

func doJSON(t *testing.T, method, url, key, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
}

func TestHTTPAuthZ(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	schemaBody := `{"type":"object","fields":[{"name":"name","type":"string","required":true}]}`

	// reader 越权写 -> 403
	resp := doJSON(t, http.MethodPost, ts.URL+"/topics/form/versions", "reader-key", schemaBody)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reader 写操作期望 403，实际 %d", resp.StatusCode)
	}
	resp.Body.Close()
	// 无效 key -> 401
	resp = doJSON(t, http.MethodPost, ts.URL+"/topics/form/versions", "bad-key", schemaBody)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无效 key 期望 401，实际 %d", resp.StatusCode)
	}
	resp.Body.Close()
	// writer 正常注册 -> 201
	resp = doJSON(t, http.MethodPost, ts.URL+"/topics/form/versions", "writer-key", schemaBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("writer 注册期望 201，实际 %d", resp.StatusCode)
	}
	resp.Body.Close()
	// reader 可以读
	resp = doJSON(t, http.MethodGet, ts.URL+"/topics/form/versions/latest", "reader-key", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reader 读期望 200，实际 %d", resp.StatusCode)
	}
	resp.Body.Close()
	// writer 不能删除（仅 admin）
	resp = doJSON(t, http.MethodDelete, ts.URL+"/topics/form/versions/1", "writer-key", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("writer 删除期望 403，实际 %d", resp.StatusCode)
	}
	resp.Body.Close()
	// admin 可以删除
	resp = doJSON(t, http.MethodDelete, ts.URL+"/topics/form/versions/1", "admin-key", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin 删除期望 200，实际 %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHTTPValidate(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	schemaBody := `{"type":"object","fields":[{"name":"name","type":"string","required":true},{"name":"age","type":"integer","minimum":0,"maximum":150}]}`
	resp := doJSON(t, http.MethodPost, ts.URL+"/topics/form/versions", "writer-key", schemaBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("注册期望 201，实际 %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 校验非法实例：缺 name、age 超范围
	resp = doJSON(t, http.MethodPost, ts.URL+"/topics/form/validate", "reader-key", `{"age":200}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("校验期望 200，实际 %d", resp.StatusCode)
	}
	var result struct {
		Valid  bool `json:"valid"`
		Errors []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	decodeBody(t, resp, &result)
	if !result.Valid {
		// 预期失败
	} else {
		t.Fatal("期望校验失败")
	}
	if len(result.Errors) != 2 {
		t.Fatalf("期望汇总 2 个错误，实际 %d: %+v", len(result.Errors), result.Errors)
	}
}

func TestHTTPCompatibilityAPI(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	schemaBody := `{"type":"object","fields":[{"name":"name","type":"string","required":true}]}`
	resp := doJSON(t, http.MethodPost, ts.URL+"/topics/form/versions", "writer-key", schemaBody)
	resp.Body.Close()

	// 预校验：新增必填字段不兼容
	resp = doJSON(t, http.MethodPost, ts.URL+"/topics/form/compatibility", "writer-key",
		`{"type":"object","fields":[{"name":"name","type":"string","required":true},{"name":"extra","type":"string","required":true}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("兼容性预校验期望 200，实际 %d", resp.StatusCode)
	}
	var cr struct {
		Compatible bool     `json:"compatible"`
		Errors     []string `json:"errors"`
	}
	decodeBody(t, resp, &cr)
	if cr.Compatible {
		t.Fatal("期望判定为不兼容")
	}

	// admin 设置为 FULL
	resp = doJSON(t, http.MethodPut, ts.URL+"/topics/form/compatibility", "admin-key", `{"compatibility":"FULL"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("设置兼容性期望 200，实际 %d", resp.StatusCode)
	}
	resp.Body.Close()
	// writer 无权设置
	resp = doJSON(t, http.MethodPut, ts.URL+"/topics/form/compatibility", "writer-key", `{"compatibility":"FULL"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("writer 设置兼容性期望 403，实际 %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHTTPListTopicsAndVersions(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	resp := doJSON(t, http.MethodPost, ts.URL+"/topics/a/versions", "writer-key",
		`{"type":"object","fields":[{"name":"x","type":"string"}]}`)
	resp.Body.Close()
	resp = doJSON(t, http.MethodPost, ts.URL+"/topics/b/versions", "writer-key",
		`{"type":"object","fields":[{"name":"y","type":"string"}]}`)
	resp.Body.Close()

	resp = doJSON(t, http.MethodGet, ts.URL+"/topics", "reader-key", "")
	var topics struct {
		Topics []string `json:"topics"`
	}
	decodeBody(t, resp, &topics)
	if len(topics.Topics) != 2 || topics.Topics[0] != "a" || topics.Topics[1] != "b" {
		t.Fatalf("主题列表异常: %v", topics.Topics)
	}

	resp = doJSON(t, http.MethodGet, ts.URL+"/topics/a/versions", "reader-key", "")
	var versions struct {
		Versions []int `json:"versions"`
	}
	decodeBody(t, resp, &versions)
	if len(versions.Versions) != 1 || versions.Versions[0] != 1 {
		t.Fatalf("版本列表异常: %v", versions.Versions)
	}
}

func TestHTTPAudit(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	resp := doJSON(t, http.MethodPost, ts.URL+"/topics/form/versions", "writer-key",
		`{"type":"object","fields":[{"name":"name","type":"string"}]}`)
	resp.Body.Close()

	// reader 无权查看审计
	resp = doJSON(t, http.MethodGet, ts.URL+"/audit", "reader-key", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reader 查看审计期望 403，实际 %d", resp.StatusCode)
	}
	resp.Body.Close()
	// admin 可以查看
	resp = doJSON(t, http.MethodGet, ts.URL+"/audit", "admin-key", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin 查看审计期望 200，实际 %d", resp.StatusCode)
	}
	var audit struct {
		Entries []struct {
			Caller  string `json:"caller"`
			Action  string `json:"action"`
			Topic   string `json:"topic"`
			Version int    `json:"version"`
		} `json:"entries"`
	}
	decodeBody(t, resp, &audit)
	if len(audit.Entries) != 1 || audit.Entries[0].Caller != "writer-key" || audit.Entries[0].Action != "register" {
		t.Fatalf("审计记录异常: %+v", audit.Entries)
	}
}
