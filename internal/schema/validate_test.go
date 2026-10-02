package schema

import (
	"strings"
	"testing"
)

func strField(name string, required bool) Field {
	return Field{Name: name, Type: TypeString, Required: required}
}

func intField(name string, required bool) Field {
	return Field{Name: name, Type: TypeInteger, Required: required}
}

func TestValidateRequiredAndType(t *testing.T) {
	s := &Schema{
		Type: TypeObject,
		Fields: []Field{
			strField("name", true),
			intField("age", true),
		},
	}
	// 缺少必填字段 + 类型错误
	res := s.Validate(map[string]any{"name": 123}, nil)
	if res.Valid {
		t.Fatal("期望校验失败")
	}
	assertHasError(t, res, "name", "string")
	assertHasError(t, res, "age", "缺少必填字段")
	if len(res.Errors) != 2 {
		t.Fatalf("期望汇总 2 个错误，实际 %d: %v", len(res.Errors), res.Errors)
	}
}

func TestValidateRangeEnumPattern(t *testing.T) {
	min, max := 0.0, 150.0
	minLen, maxLen := 2, 10
	pattern := `^[a-z]+$`
	s := &Schema{
		Type: TypeObject,
		Fields: []Field{
			{Name: "age", Type: TypeInteger, Minimum: &min, Maximum: &max},
			{Name: "gender", Type: TypeString, Enum: []any{"male", "female"}},
			{Name: "code", Type: TypeString, MinLength: &minLen, MaxLength: &maxLen, Pattern: &pattern},
		},
	}
	res := s.Validate(map[string]any{
		"age":    200,
		"gender": "unknown",
		"code":   "ABC",
	}, nil)
	if res.Valid {
		t.Fatal("期望校验失败")
	}
	assertHasError(t, res, "age", "maximum")
	assertHasError(t, res, "gender", "枚举")
	assertHasError(t, res, "code", "pattern")
}

func TestValidateNestedAndArray(t *testing.T) {
	s := &Schema{
		Type: TypeObject,
		Fields: []Field{
			{
				Name: "address", Type: TypeObject, Required: true,
				Fields: []Field{
					strField("city", true),
					{Name: "zip", Type: TypeString, Pattern: strPtr(`^\d{6}$`)},
				},
			},
			{
				Name: "tags", Type: TypeArray,
				Items: &Field{Name: "_", Type: TypeString},
			},
		},
	}
	res := s.Validate(map[string]any{
		"address": map[string]any{"zip": "abc"},
		"tags":    []any{"ok", 123},
	}, nil)
	if res.Valid {
		t.Fatal("期望校验失败")
	}
	assertHasError(t, res, "address.city", "缺少必填字段")
	assertHasError(t, res, "address.zip", "pattern")
	assertHasError(t, res, "tags[1]", "string")
}

func TestValidateRef(t *testing.T) {
	// 被引用的主题 schema：联系人
	contact := &Schema{
		Type: TypeObject,
		Fields: []Field{
			strField("name", true),
			{Name: "phone", Type: TypeString, Pattern: strPtr(`^\d{11}$`)},
		},
	}
	resolver := func(ref string) (*Schema, error) {
		if ref == "contact" {
			return contact, nil
		}
		return nil, errNotFound(ref)
	}
	s := &Schema{
		Type: TypeObject,
		Fields: []Field{
			{Name: "contact", Type: TypeObject, Ref: "contact"},
		},
	}
	res := s.Validate(map[string]any{
		"contact": map[string]any{"phone": "123"},
	}, resolver)
	if res.Valid {
		t.Fatal("期望校验失败")
	}
	assertHasError(t, res, "contact.name", "缺少必填字段")
	assertHasError(t, res, "contact.phone", "pattern")

	// 引用解析失败
	s2 := &Schema{Type: TypeObject, Fields: []Field{{Name: "c", Ref: "missing"}}}
	res2 := s2.Validate(map[string]any{"c": map[string]any{}}, resolver)
	if res2.Valid {
		t.Fatal("期望引用失败")
	}
	assertHasError(t, res2, "c", "解析失败")
}

func TestValidateValidInstance(t *testing.T) {
	min, max := 0.0, 150.0
	s := &Schema{
		Type: TypeObject,
		Fields: []Field{
			strField("name", true),
			{Name: "age", Type: TypeInteger, Minimum: &min, Maximum: &max},
			{Name: "tags", Type: TypeArray, Items: &Field{Name: "_", Type: TypeString}},
		},
	}
	res := s.Validate(map[string]any{
		"name": "张三",
		"age":  30,
		"tags": []any{"a", "b"},
	}, nil)
	if !res.Valid {
		t.Fatalf("期望校验通过，实际错误: %v", res.Errors)
	}
}

func TestValidateStructure(t *testing.T) {
	cases := []struct {
		name string
		s    *Schema
		want string
	}{
		{"nil", nil, "模式不能为空"},
		{"empty type", &Schema{Type: ""}, "类型不能为空"},
		{"object no fields", &Schema{Type: TypeObject}, "fields"},
		{"array no items", &Schema{Type: TypeArray}, "items"},
		{"bad ref", &Schema{Type: TypeString, Ref: "topic:abc"}, "版本号非法"},
		{"bad pattern", &Schema{Type: TypeString, Pattern: strPtr("[")}, "pattern"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := ValidateStructure(c.s)
			if len(errs) == 0 {
				t.Fatalf("期望结构非法")
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), c.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("期望错误包含 %q，实际 %v", c.want, errs)
			}
		})
	}
}

func assertHasError(t *testing.T, res *ValidateResult, path, contains string) {
	t.Helper()
	for _, e := range res.Errors {
		if e.Path == path && strings.Contains(e.Message, contains) {
			return
		}
	}
	t.Fatalf("期望路径 %q 的错误包含 %q，实际错误: %v", path, contains, res.Errors)
}

func strPtr(s string) *string { return &s }

type notFoundErr struct{ ref string }

func (e *notFoundErr) Error() string { return "引用的版本不存在: " + e.ref }

func errNotFound(ref string) error { return &notFoundErr{ref: ref} }
