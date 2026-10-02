package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shikejing/schema-registry-go/internal/schema"
)

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "registry.json")
	st := NewFileStore(path)

	snap := &Snapshot{
		Topics: map[string]*TopicSnapshot{
			"form": {
				Versions: map[int]*RegisteredSchema{
					1: {
						Topic:   "form",
						Version: 1,
						Schema: &schema.Schema{
							Type:   schema.TypeObject,
							Fields: []schema.Field{{Name: "name", Type: schema.TypeString, Required: true}},
						},
						CreatedBy: "writer-key",
					},
				},
			},
		},
	}
	if err := st.Save(snap); err != nil {
		t.Fatal(err)
	}
	// 无 .tmp 残留
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("存在临时文件残留")
	}
	loaded, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Topics["form"].Versions) != 1 {
		t.Fatalf("加载结果异常: %+v", loaded.Topics)
	}
	rs := loaded.Topics["form"].Versions[1]
	if rs.Schema.Fields[0].Name != "name" || rs.CreatedBy != "writer-key" {
		t.Fatalf("版本内容恢复异常: %+v", rs)
	}
}

func TestLoadMissingFile(t *testing.T) {
	st := NewFileStore(filepath.Join(t.TempDir(), "missing.json"))
	snap, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Topics) != 0 {
		t.Fatal("无文件时应返回空快照")
	}
}
