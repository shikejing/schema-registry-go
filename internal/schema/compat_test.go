package schema

import (
	"strings"
	"testing"
)

func objSchema(fields ...Field) *Schema {
	return &Schema{Type: TypeObject, Fields: fields}
}

func TestBackwardCompatibility(t *testing.T) {
	old := objSchema(
		strField("name", true),
		Field{Name: "age", Type: TypeInteger, Enum: []any{1, 2, 3}},
	)
	cases := []struct {
		name string
		new  *Schema
		want bool
	}{
		{"新增可选字段", objSchema(strField("name", true), Field{Name: "age", Type: TypeInteger, Enum: []any{1, 2, 3}}, strField("extra", false)), true},
		{"新增必填字段", objSchema(strField("name", true), Field{Name: "age", Type: TypeInteger, Enum: []any{1, 2, 3}}, strField("extra", true)), false},
		{"删除字段", objSchema(strField("name", true)), true},
		{"修改字段类型", objSchema(strField("name", true), Field{Name: "age", Type: TypeString}), false},
		{"收窄枚举", objSchema(strField("name", true), Field{Name: "age", Type: TypeInteger, Enum: []any{1, 2}}), false},
		{"放宽枚举", objSchema(strField("name", true), Field{Name: "age", Type: TypeInteger, Enum: []any{1, 2, 3, 4}}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := CheckCompatibility(c.new, old, CompatibilityBackward, nil)
			if res.Compatible != c.want {
				t.Fatalf("BACKWARD 期望 compatible=%v，实际 %v: %s", c.want, res.Compatible, strings.Join(res.Errors, "; "))
			}
		})
	}
}

func TestForwardCompatibility(t *testing.T) {
	old := objSchema(
		strField("name", true),
		Field{Name: "age", Type: TypeInteger, Enum: []any{1, 2, 3}},
	)
	cases := []struct {
		name string
		new  *Schema
		want bool
	}{
		{"新增可选字段", objSchema(strField("name", true), Field{Name: "age", Type: TypeInteger, Enum: []any{1, 2, 3}}, strField("extra", false)), true},
		{"必填字段变为可选", objSchema(strField("name", false)), false},
		{"删除必填字段", objSchema(Field{Name: "other", Type: TypeString}), false},
		{"删除可选字段", objSchema(strField("name", true)), true},
		{"修改字段类型", objSchema(strField("name", true), Field{Name: "age", Type: TypeString}), false},
		{"放宽枚举", objSchema(strField("name", true), Field{Name: "age", Type: TypeInteger, Enum: []any{1, 2, 3, 4}}), false},
		{"收窄枚举", objSchema(strField("name", true), Field{Name: "age", Type: TypeInteger, Enum: []any{1}}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := CheckCompatibility(c.new, old, CompatibilityForward, nil)
			if res.Compatible != c.want {
				t.Fatalf("FORWARD 期望 compatible=%v，实际 %v: %s", c.want, res.Compatible, strings.Join(res.Errors, "; "))
			}
		})
	}
}

func TestFullCompatibility(t *testing.T) {
	old := objSchema(
		strField("name", true),
		intField("age", false),
	)
	cases := []struct {
		name string
		new  *Schema
		want bool
	}{
		{"完全相同", objSchema(strField("name", true), intField("age", false)), true},
		{"新增可选字段", objSchema(strField("name", true), intField("age", false), strField("extra", false)), false},
		{"删除可选字段", objSchema(strField("name", true)), false},
		{"类型变更", objSchema(strField("name", true), Field{Name: "age", Type: TypeString}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := CheckCompatibility(c.new, old, CompatibilityFull, nil)
			if res.Compatible != c.want {
				t.Fatalf("FULL 期望 compatible=%v，实际 %v: %s", c.want, res.Compatible, strings.Join(res.Errors, "; "))
			}
		})
	}
}

func TestRangeCompatibility(t *testing.T) {
	oldMin, oldMax := 0.0, 100.0
	old := &Schema{Type: TypeObject, Fields: []Field{
		{Name: "v", Type: TypeNumber, Minimum: &oldMin, Maximum: &oldMax},
	}}
	// 向后：收紧上界不允许，放宽上界允许
	newMaxTight := 50.0
	newMaxWide := 200.0
	res := CheckCompatibility(
		&Schema{Type: TypeObject, Fields: []Field{{Name: "v", Type: TypeNumber, Minimum: &oldMin, Maximum: &newMaxTight}}},
		old, CompatibilityBackward, nil)
	if res.Compatible {
		t.Fatal("BACKWARD 收紧 maximum 应不兼容")
	}
	res = CheckCompatibility(
		&Schema{Type: TypeObject, Fields: []Field{{Name: "v", Type: TypeNumber, Minimum: &oldMin, Maximum: &newMaxWide}}},
		old, CompatibilityBackward, nil)
	if !res.Compatible {
		t.Fatalf("BACKWARD 放宽 maximum 应兼容: %v", res.Errors)
	}
	// 向前：放宽上界不允许，收紧上界允许
	res = CheckCompatibility(
		&Schema{Type: TypeObject, Fields: []Field{{Name: "v", Type: TypeNumber, Minimum: &oldMin, Maximum: &newMaxWide}}},
		old, CompatibilityForward, nil)
	if res.Compatible {
		t.Fatal("FORWARD 放宽 maximum 应不兼容")
	}
	res = CheckCompatibility(
		&Schema{Type: TypeObject, Fields: []Field{{Name: "v", Type: TypeNumber, Minimum: &oldMin, Maximum: &newMaxTight}}},
		old, CompatibilityForward, nil)
	if !res.Compatible {
		t.Fatalf("FORWARD 收紧 maximum 应兼容: %v", res.Errors)
	}
}

func TestNoneCompatibility(t *testing.T) {
	old := objSchema(strField("a", true))
	new := objSchema(Field{Name: "a", Type: TypeInteger})
	res := CheckCompatibility(new, old, CompatibilityNone, nil)
	if !res.Compatible {
		t.Fatal("NONE 模式不应做兼容性检查")
	}
}

func TestCompatibilityWithRefs(t *testing.T) {
	// 被引用模式
	contact := objSchema(strField("name", true), strField("phone", false))
	resolver := func(ref string) (*Schema, error) {
		if ref == "contact" {
			return contact, nil
		}
		return nil, errNotFound(ref)
	}
	// 新模式引用 contact，与旧模式完全一致 -> 兼容
	old := &Schema{Type: TypeObject, Fields: []Field{
		strField("id", true),
		{Name: "contact", Type: TypeObject, Ref: "contact"},
	}}
	new := &Schema{Type: TypeObject, Fields: []Field{
		strField("id", true),
		{Name: "contact", Type: TypeObject, Ref: "contact"},
	}}
	res := CheckCompatibility(new, old, CompatibilityBackward, resolver)
	if !res.Compatible {
		t.Fatalf("相同引用模式应兼容: %v", res.Errors)
	}
	// 新模式在引用字段上新增必填字段 -> 不兼容
	new2 := &Schema{Type: TypeObject, Fields: []Field{
		strField("id", true),
		{Name: "contact", Type: TypeObject, Ref: "contact"},
		strField("extra", true),
	}}
	res = CheckCompatibility(new2, old, CompatibilityBackward, resolver)
	if res.Compatible {
		t.Fatal("新增必填字段应不兼容")
	}
}
