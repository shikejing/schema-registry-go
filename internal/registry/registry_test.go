package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/shikejing/schema-registry-go/internal/auth"
	"github.com/shikejing/schema-registry-go/internal/schema"
	"github.com/shikejing/schema-registry-go/internal/store"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	st := store.NewFileStore(filepath.Join(dir, "registry.json"))
	auditLog := auth.NewAuditLog()
	authenticator := auth.NewAuthenticator(auditLog)
	authenticator.AddKey("admin-key", auth.RoleAdmin)
	authenticator.AddKey("writer-key", auth.RoleWriter)
	authenticator.AddKey("reader-key", auth.RoleReader)
	reg, err := New(st, authenticator, auditLog)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func simpleSchema(name string, required bool) *schema.Schema {
	return &schema.Schema{
		Type: schema.TypeObject,
		Fields: []schema.Field{
			{Name: name, Type: schema.TypeString, Required: required},
		},
	}
}

func TestRegisterAndGet(t *testing.T) {
	reg := newTestRegistry(t)
	rs1, err := reg.Register(RegisterInput{Topic: "form", Schema: simpleSchema("name", true), Caller: "writer-key"})
	if err != nil {
		t.Fatal(err)
	}
	if rs1.Version != 1 {
		t.Fatalf("期望版本 1，实际 %d", rs1.Version)
	}
	rs2, err := reg.Register(RegisterInput{Topic: "form", Schema: simpleSchema("name", true), Caller: "writer-key"})
	if err != nil {
		t.Fatal(err)
	}
	if rs2.Version != 2 {
		t.Fatalf("期望版本 2，实际 %d", rs2.Version)
	}
	// 最新版本
	latest, err := reg.Get("form", 0)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != 2 {
		t.Fatalf("期望最新版本为 2，实际 %d", latest.Version)
	}
	// 指定版本
	got1, err := reg.Get("form", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got1.Version != 1 || got1.Schema.Fields[0].Name != "name" {
		t.Fatalf("版本 1 内容异常: %+v", got1)
	}
	versions, err := reg.Versions("form")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0] != 1 || versions[1] != 2 {
		t.Fatalf("版本列表异常: %v", versions)
	}
}

func TestVersionImmutable(t *testing.T) {
	reg := newTestRegistry(t)
	rs1, _ := reg.Register(RegisterInput{Topic: "form", Schema: simpleSchema("name", true), Caller: "writer-key"})
	// 注册 v2（新增可选字段，向后兼容）
	s2 := simpleSchema("name", true)
	s2.Fields = append(s2.Fields, schema.Field{Name: "extra", Type: schema.TypeString, Required: false})
	reg.Register(RegisterInput{Topic: "form", Schema: s2, Caller: "writer-key"})
	// v1 内容不得被改写
	got1, _ := reg.Get("form", 1)
	if len(got1.Schema.Fields) != 1 || got1.Schema.Fields[0].Name != "name" {
		t.Fatalf("历史版本内容被改写: %+v", got1.Schema.Fields)
	}
	_ = rs1
}

func TestConcurrentRegister(t *testing.T) {
	reg := newTestRegistry(t)
	n := 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := reg.Register(RegisterInput{
				Topic:  "form",
				Schema: simpleSchema(fmt.Sprintf("field%d", i), false),
				Caller: "writer-key",
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发注册失败: %v", err)
		}
	}
	versions, err := reg.Versions("form")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != n {
		t.Fatalf("期望 %d 个版本，实际 %d", n, len(versions))
	}
	seen := map[int]bool{}
	for _, v := range versions {
		if seen[v] {
			t.Fatalf("版本号重复: %d", v)
		}
		seen[v] = true
	}
	for i := 1; i <= n; i++ {
		if !seen[i] {
			t.Fatalf("版本号存在空洞，缺少 %d", i)
		}
	}
}

func TestCompatibilityRejection(t *testing.T) {
	reg := newTestRegistry(t)
	reg.Register(RegisterInput{Topic: "form", Schema: simpleSchema("name", true), Caller: "writer-key"})

	// 新增必填字段：向后兼容拒绝
	s2 := simpleSchema("name", true)
	s2.Fields = append(s2.Fields, schema.Field{Name: "extra", Type: schema.TypeString, Required: true})
	_, err := reg.Register(RegisterInput{Topic: "form", Schema: s2, Caller: "writer-key"})
	if err == nil {
		t.Fatal("期望兼容性校验拒绝注册")
	}
	// 不得产生新版本
	versions, _ := reg.Versions("form")
	if len(versions) != 1 {
		t.Fatalf("被拒绝的注册不应留下版本，实际版本: %v", versions)
	}
	// v1 内容不变
	got1, _ := reg.Get("form", 1)
	if len(got1.Schema.Fields) != 1 {
		t.Fatalf("被拒绝的注册改动了已有版本")
	}

	// 改为 NONE 后应允许
	reg.SetCompatibility("form", schema.CompatibilityNone, "admin-key")
	_, err = reg.Register(RegisterInput{Topic: "form", Schema: s2, Caller: "writer-key"})
	if err != nil {
		t.Fatalf("NONE 模式应允许注册: %v", err)
	}
}

func TestCrossTopicRef(t *testing.T) {
	reg := newTestRegistry(t)
	// 被引用主题
	_, err := reg.Register(RegisterInput{Topic: "contact", Schema: simpleSchema("name", true), Caller: "writer-key"})
	if err != nil {
		t.Fatal(err)
	}
	// 跨主题引用
	order := &schema.Schema{
		Type: schema.TypeObject,
		Fields: []schema.Field{
			{Name: "id", Type: schema.TypeString, Required: true},
			{Name: "contact", Type: schema.TypeObject, Ref: "contact"},
		},
	}
	_, err = reg.Register(RegisterInput{Topic: "order", Schema: order, Caller: "writer-key"})
	if err != nil {
		t.Fatalf("跨主题引用应注册成功: %v", err)
	}
	// 引用不存在的主题
	bad := &schema.Schema{Type: schema.TypeObject, Fields: []schema.Field{{Name: "c", Ref: "nope"}}}
	_, err = reg.Register(RegisterInput{Topic: "x", Schema: bad, Caller: "writer-key"})
	if err == nil {
		t.Fatal("引用不存在的主题应被拒绝")
	}
	// 引用不存在的版本
	bad2 := &schema.Schema{Type: schema.TypeObject, Fields: []schema.Field{{Name: "c", Ref: "contact:99"}}}
	_, err = reg.Register(RegisterInput{Topic: "y", Schema: bad2, Caller: "writer-key"})
	if err == nil {
		t.Fatal("引用不存在的版本应被拒绝")
	}
}

func TestCycleRefRejected(t *testing.T) {
	reg := newTestRegistry(t)
	// a v1 无引用
	reg.Register(RegisterInput{Topic: "a", Schema: simpleSchema("name", true), Caller: "writer-key"})
	// b v1 引用 a
	b := &schema.Schema{Type: schema.TypeObject, Fields: []schema.Field{{Name: "a", Type: schema.TypeObject, Ref: "a"}}}
	if _, err := reg.Register(RegisterInput{Topic: "b", Schema: b, Caller: "writer-key"}); err != nil {
		t.Fatal(err)
	}
	// a v2 引用 b -> 构成环 a->b->a，拒绝
	a2 := &schema.Schema{Type: schema.TypeObject, Fields: []schema.Field{{Name: "b", Type: schema.TypeObject, Ref: "b"}}}
	_, err := reg.Register(RegisterInput{Topic: "a", Schema: a2, Caller: "writer-key"})
	if err == nil {
		t.Fatal("期望循环引用被拒绝")
	}
	// 直接自引用
	self := &schema.Schema{Type: schema.TypeObject, Fields: []schema.Field{{Name: "self", Type: schema.TypeObject, Ref: "self"}}}
	_, err = reg.Register(RegisterInput{Topic: "self", Schema: self, Caller: "writer-key"})
	if err == nil {
		t.Fatal("期望自引用被拒绝")
	}
}

func TestDeleteProtection(t *testing.T) {
	reg := newTestRegistry(t)
	reg.Register(RegisterInput{Topic: "a", Schema: simpleSchema("name", true), Caller: "writer-key"})
	b := &schema.Schema{Type: schema.TypeObject, Fields: []schema.Field{{Name: "a", Type: schema.TypeObject, Ref: "a"}}}
	reg.Register(RegisterInput{Topic: "b", Schema: b, Caller: "writer-key"})

	// a:1 被 b:1 引用，拒绝删除
	err := reg.Delete("a", 1, "admin-key")
	if err == nil {
		t.Fatal("期望删除被引用版本被拒绝")
	}
	// 删除引用方 b:1 应成功
	if err := reg.Delete("b", 1, "admin-key"); err != nil {
		t.Fatalf("删除未被引用的版本应成功: %v", err)
	}
	// 引用关系解除后可删除
	if err := reg.Delete("a", 1, "admin-key"); err != nil {
		t.Fatalf("引用解除后应可删除: %v", err)
	}
}

func TestRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	st := store.NewFileStore(path)
	auditLog := auth.NewAuditLog()
	authenticator := auth.NewAuthenticator(auditLog)
	authenticator.AddKey("writer-key", auth.RoleWriter)
	authenticator.AddKey("admin-key", auth.RoleAdmin)

	reg, err := New(st, authenticator, auditLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SetCompatibility("form", schema.CompatibilityFull, "admin-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Register(RegisterInput{Topic: "form", Schema: simpleSchema("name", true), Caller: "writer-key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Register(RegisterInput{Topic: "form", Schema: simpleSchema("name", true), Caller: "writer-key"}); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：重新从同一文件加载
	st2 := store.NewFileStore(path)
	auditLog2 := auth.NewAuditLog()
	auth2 := auth.NewAuthenticator(auditLog2)
	auth2.AddKey("writer-key", auth.RoleWriter)
	reg2, err := New(st2, auth2, auditLog2)
	if err != nil {
		t.Fatal(err)
	}
	versions, _ := reg2.Versions("form")
	if len(versions) != 2 {
		t.Fatalf("重启后期望 2 个版本，实际 %v", versions)
	}
	got1, err := reg2.Get("form", 1)
	if err != nil || got1.Schema.Fields[0].Name != "name" {
		t.Fatalf("版本 1 未正确恢复: %v", err)
	}
	if got1.CreatedBy != "writer-key" {
		t.Fatalf("版本元数据未恢复: %+v", got1)
	}
	if mode := reg2.GetCompatibility("form"); mode != schema.CompatibilityFull {
		t.Fatalf("兼容性配置未恢复，期望 FULL，实际 %s", mode)
	}
	entries := reg2.AuditEntries()
	if len(entries) < 3 {
		t.Fatalf("审计记录未完整恢复，期望 >=3 条，实际 %d", len(entries))
	}
	// 重启后注册继续递增
	rs3, err := reg2.Register(RegisterInput{Topic: "form", Schema: simpleSchema("name", true), Caller: "writer-key"})
	if err != nil {
		t.Fatal(err)
	}
	if rs3.Version != 3 {
		t.Fatalf("重启后期望新版本为 3，实际 %d", rs3.Version)
	}
	// 无 .tmp 残留
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("存在未完成注册的临时文件残留")
	}
}

func TestAuditTrail(t *testing.T) {
	reg := newTestRegistry(t)
	reg.Register(RegisterInput{Topic: "form", Schema: simpleSchema("name", true), Caller: "writer-key"})
	reg.Delete("form", 1, "admin-key")
	entries := reg.AuditEntries()
	if len(entries) != 2 {
		t.Fatalf("期望 2 条审计记录，实际 %d", len(entries))
	}
	if entries[0].Action != "register" || entries[0].Caller != "writer-key" || entries[0].Version != 1 {
		t.Fatalf("注册审计记录异常: %+v", entries[0])
	}
	if entries[1].Action != "delete" || entries[1].Caller != "admin-key" {
		t.Fatalf("删除审计记录异常: %+v", entries[1])
	}
	if entries[0].Time.IsZero() {
		t.Fatal("审计记录缺少时间")
	}
}
