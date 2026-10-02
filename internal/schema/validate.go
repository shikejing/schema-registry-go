package schema

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Resolver 解析跨主题引用：ref 形如 "topic"（最新版本）或 "topic:2"（指定版本）。
type Resolver func(ref string) (*Schema, error)

// ValidateStructure 校验模式自身结构是否合法（注册时调用）。
func ValidateStructure(s *Schema) []error {
	var errs []error
	if s == nil {
		return []error{fmt.Errorf("模式不能为空")}
	}
	if s.Type == "" {
		errs = append(errs, fmt.Errorf("字段类型不能为空"))
	}
	// 引用模式的结构由被引用模式决定，不要求内联 fields/items。
	if s.Ref != "" {
		if _, _, err := ParseRef(s.Ref); err != nil {
			errs = append(errs, err)
		}
		return errs
	}
	switch s.Type {
	case TypeObject:
		if len(s.Fields) == 0 {
			errs = append(errs, fmt.Errorf("object 类型必须定义 fields"))
		}
		names := map[string]bool{}
		for i, f := range s.Fields {
			if f.Name == "" {
				errs = append(errs, fmt.Errorf("fields[%d] 缺少 name", i))
				continue
			}
			if names[f.Name] {
				errs = append(errs, fmt.Errorf("字段 %q 重复定义", f.Name))
			}
			names[f.Name] = true
			errs = append(errs, validateFieldStructure(f, "fields."+f.Name)...)
		}
	case TypeArray:
		if s.Items == nil {
			errs = append(errs, fmt.Errorf("array 类型必须定义 items"))
		} else {
			errs = append(errs, validateFieldStructure(*s.Items, "items")...)
		}
	case TypeString, TypeInteger, TypeNumber, TypeBoolean:
		// 基础类型无需额外结构
	default:
		errs = append(errs, fmt.Errorf("未知类型 %q", s.Type))
	}
	if s.Pattern != nil {
		if _, err := regexp.Compile(*s.Pattern); err != nil {
			errs = append(errs, fmt.Errorf("pattern 不是合法正则: %v", err))
		}
	}
	return errs
}

func validateFieldStructure(f Field, path string) []error {
	var errs []error
	if f.Type == "" && f.Ref == "" {
		return []error{fmt.Errorf("%s: 字段类型不能为空", path)}
	}
	// 引用字段的结构由被引用模式决定，不要求内联 fields/items。
	if f.Ref != "" {
		if _, _, err := ParseRef(f.Ref); err != nil {
			errs = append(errs, fmt.Errorf("%s: %v", path, err))
		}
		return errs
	}
	switch f.Type {
	case TypeObject:
		if len(f.Fields) == 0 {
			errs = append(errs, fmt.Errorf("%s: object 类型必须定义 fields", path))
		}
		names := map[string]bool{}
		for _, cf := range f.Fields {
			if names[cf.Name] {
				errs = append(errs, fmt.Errorf("%s: 字段 %q 重复定义", path, cf.Name))
			}
			names[cf.Name] = true
			errs = append(errs, validateFieldStructure(cf, path+"."+cf.Name)...)
		}
	case TypeArray:
		if f.Items == nil {
			errs = append(errs, fmt.Errorf("%s: array 类型必须定义 items", path))
		} else {
			errs = append(errs, validateFieldStructure(*f.Items, path+"[].")...)
		}
	case TypeString, TypeInteger, TypeNumber, TypeBoolean, "":
	default:
		errs = append(errs, fmt.Errorf("%s: 未知类型 %q", path, f.Type))
	}
	if f.Pattern != nil {
		if _, err := regexp.Compile(*f.Pattern); err != nil {
			errs = append(errs, fmt.Errorf("%s: pattern 不是合法正则: %v", path, err))
		}
	}
	if f.Ref != "" {
		if _, _, err := ParseRef(f.Ref); err != nil {
			errs = append(errs, fmt.Errorf("%s: %v", path, err))
		}
	}
	return errs
}

// ParseRef 解析 "topic" 或 "topic:version" 形式的引用。
func ParseRef(ref string) (topic string, version int, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", 0, fmt.Errorf("引用不能为空")
	}
	if i := strings.Index(ref, ":"); i >= 0 {
		topic = ref[:i]
		v := ref[i+1:]
		if topic == "" {
			return "", 0, fmt.Errorf("引用 %q 缺少主题名", ref)
		}
		version, err = strconv.Atoi(v)
		if err != nil || version <= 0 {
			return "", 0, fmt.Errorf("引用 %q 的版本号非法: %q", ref, v)
		}
		return topic, version, nil
	}
	return ref, 0, nil
}

// Validate 按模式校验实例数据，汇总全部错误。
func (s *Schema) Validate(instance any, resolver Resolver) *ValidateResult {
	res := &ValidateResult{Valid: true}
	if instance == nil {
		res.Errors = append(res.Errors, ValidationError{Path: "$", Message: "实例不能为 null"})
		res.Valid = false
		return res
	}
	v := newValidator(resolver)
	v.validateSchemaValue(s, instance, "$", res)
	if len(res.Errors) > 0 {
		res.Valid = false
	}
	return res
}

type validator struct {
	patterns map[string]*regexp.Regexp
	resolver Resolver
}

func newValidator(resolver Resolver) *validator {
	return &validator{patterns: map[string]*regexp.Regexp{}, resolver: resolver}
}

func (v *validator) compilePattern(p string) *regexp.Regexp {
	if re, ok := v.patterns[p]; ok {
		return re
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil
	}
	v.patterns[p] = re
	return re
}

// validateSchemaValue 校验一个值，schema 可能带 Ref（引用优先）。
func (v *validator) validateSchemaValue(s *Schema, val any, path string, res *ValidateResult) {
	if s == nil {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: "模式为空，无法校验"})
		return
	}
	if s.Ref != "" {
		refSchema, err := v.resolve(s.Ref)
		if err != nil {
			res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("引用 %q 解析失败: %v", s.Ref, err)})
			return
		}
		v.validateSchemaValue(refSchema, val, path, res)
		return
	}
	v.validateValue(val, fieldFromSchema(s), path, res)
}

func (v *validator) resolve(ref string) (*Schema, error) {
	if v.resolver == nil {
		return nil, fmt.Errorf("未提供引用解析器")
	}
	return v.resolver(ref)
}

// fieldFromSchema 把根模式转换为字段结构以便复用校验逻辑。
func fieldFromSchema(s *Schema) Field {
	f := Field{
		Name:      s.Name,
		Type:      s.Type,
		Fields:    s.Fields,
		Items:     s.Items,
		Enum:      s.Enum,
		Minimum:   s.Minimum,
		Maximum:   s.Maximum,
		MinLength: s.MinLength,
		MaxLength: s.MaxLength,
		Pattern:   s.Pattern,
		Ref:       s.Ref,
	}
	return f
}

func (v *validator) validateValue(val any, f Field, path string, res *ValidateResult) {
	if f.Ref != "" {
		refSchema, err := v.resolve(f.Ref)
		if err != nil {
			res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("引用 %q 解析失败: %v", f.Ref, err)})
			return
		}
		v.validateSchemaValue(refSchema, val, path, res)
		return
	}
	if val == nil {
		// nil 是否允许由调用方通过 required 控制；非必填字段为 nil 时跳过。
		return
	}
	switch f.Type {
	case TypeObject:
		v.validateObject(val, f, path, res)
	case TypeArray:
		v.validateArray(val, f, path, res)
	case TypeString:
		v.validateString(val, f, path, res)
	case TypeInteger:
		v.validateInteger(val, f, path, res)
	case TypeNumber:
		v.validateNumber(val, f, path, res)
	case TypeBoolean:
		v.validateBoolean(val, f, path, res)
	default:
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("未知类型 %q", f.Type)})
	}
}

func (v *validator) validateObject(val any, f Field, path string, res *ValidateResult) {
	m, ok := val.(map[string]any)
	if !ok {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("期望 object，实际为 %s", typeName(val))})
		return
	}
	fieldMap := map[string]Field{}
	for _, cf := range f.Fields {
		fieldMap[cf.Name] = cf
	}
	// 必填校验（按字段名排序，保证错误顺序稳定）
	names := make([]string, 0, len(f.Fields))
	for _, cf := range f.Fields {
		names = append(names, cf.Name)
	}
	sort.Strings(names)
	for _, name := range names {
		cf := fieldMap[name]
		if cf.Required {
			if _, present := m[name]; !present {
				res.Errors = append(res.Errors, ValidationError{Path: joinPath(path, name), Message: "缺少必填字段"})
			}
		}
	}
	// 逐字段校验
	for _, name := range names {
		cf := fieldMap[name]
		child, present := m[name]
		if !present {
			continue
		}
		if child == nil {
			if cf.Required {
				res.Errors = append(res.Errors, ValidationError{Path: joinPath(path, name), Message: "必填字段不能为 null"})
			}
			continue
		}
		v.validateValue(child, cf, joinPath(path, name), res)
	}
}

func (v *validator) validateArray(val any, f Field, path string, res *ValidateResult) {
	arr, ok := val.([]any)
	if !ok {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("期望 array，实际为 %s", typeName(val))})
		return
	}
	if f.MinLength != nil && len(arr) < *f.MinLength {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("数组长度 %d 小于 minLength %d", len(arr), *f.MinLength)})
	}
	if f.MaxLength != nil && len(arr) > *f.MaxLength {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("数组长度 %d 大于 maxLength %d", len(arr), *f.MaxLength)})
	}
	if f.Items != nil {
		for i, item := range arr {
			v.validateValue(item, *f.Items, fmt.Sprintf("%s[%d]", path, i), res)
		}
	}
}

func (v *validator) validateString(val any, f Field, path string, res *ValidateResult) {
	s, ok := val.(string)
	if !ok {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("期望 string，实际为 %s", typeName(val))})
		return
	}
	if f.MinLength != nil && len([]rune(s)) < *f.MinLength {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("字符串长度 %d 小于 minLength %d", len([]rune(s)), *f.MinLength)})
	}
	if f.MaxLength != nil && len([]rune(s)) > *f.MaxLength {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("字符串长度 %d 大于 maxLength %d", len([]rune(s)), *f.MaxLength)})
	}
	if f.Pattern != nil {
		re := v.compilePattern(*f.Pattern)
		if re == nil {
			res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("pattern %q 不是合法正则", *f.Pattern)})
		} else if !re.MatchString(s) {
			res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("字符串不匹配 pattern %q", *f.Pattern)})
		}
	}
	v.checkEnum(s, f.Enum, path, res)
}

func (v *validator) validateInteger(val any, f Field, path string, res *ValidateResult) {
	n, ok := asFloat(val)
	if !ok {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("期望 integer，实际为 %s", typeName(val))})
		return
	}
	if math.Trunc(n) != n {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("期望 integer，实际为 %v", n)})
		return
	}
	v.checkRange(n, f, path, res)
	v.checkEnum(n, f.Enum, path, res)
}

func (v *validator) validateNumber(val any, f Field, path string, res *ValidateResult) {
	n, ok := asFloat(val)
	if !ok {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("期望 number，实际为 %s", typeName(val))})
		return
	}
	v.checkRange(n, f, path, res)
	v.checkEnum(n, f.Enum, path, res)
}

func (v *validator) validateBoolean(val any, f Field, path string, res *ValidateResult) {
	b, ok := val.(bool)
	if !ok {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("期望 boolean，实际为 %s", typeName(val))})
		return
	}
	v.checkEnum(b, f.Enum, path, res)
}

func (v *validator) checkRange(n float64, f Field, path string, res *ValidateResult) {
	if f.Minimum != nil && n < *f.Minimum {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("值 %v 小于 minimum %v", n, *f.Minimum)})
	}
	if f.Maximum != nil && n > *f.Maximum {
		res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("值 %v 大于 maximum %v", n, *f.Maximum)})
	}
}

func (v *validator) checkEnum(val any, enum []any, path string, res *ValidateResult) {
	if len(enum) == 0 {
		return
	}
	for _, e := range enum {
		if valuesEqual(val, e) {
			return
		}
	}
	res.Errors = append(res.Errors, ValidationError{Path: path, Message: fmt.Sprintf("值 %v 不在枚举 %v 中", val, enum)})
}

// asFloat 把 JSON 中可能出现的数值类型统一为 float64。
func asFloat(val any) (float64, bool) {
	switch n := val.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// valuesEqual 比较两个值是否相等，数值按数值比较（忽略 int/float64 差异）。
func valuesEqual(a, b any) bool {
	af, aok := asFloat(a)
	bf, bok := asFloat(b)
	if aok && bok {
		return af == bf
	}
	return a == b
}

func typeName(val any) string {
	if val == nil {
		return "null"
	}
	return fmt.Sprintf("%T", val)
}

func joinPath(base, name string) string {
	if base == "" || base == "$" {
		return name
	}
	return base + "." + name
}
