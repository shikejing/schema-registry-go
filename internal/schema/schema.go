// Package schema 定义模式（Schema）结构、实例校验与兼容性校验。
package schema

// Type 字段类型。
type Type string

const (
	TypeObject  Type = "object"
	TypeString  Type = "string"
	TypeInteger Type = "integer"
	TypeNumber  Type = "number"
	TypeBoolean Type = "boolean"
	TypeArray   Type = "array"
)

// Field 描述对象中的一个字段，也可用于描述数组元素类型。
type Field struct {
	Name      string   `json:"name"`
	Type      Type     `json:"type"`
	Required  bool     `json:"required,omitempty"`
	Fields    []Field  `json:"fields,omitempty"`    // TypeObject 时的子字段
	Items     *Field   `json:"items,omitempty"`     // TypeArray 时的元素定义
	Enum      []any    `json:"enum,omitempty"`      // 枚举约束
	Minimum   *float64 `json:"minimum,omitempty"`   // 数值下界
	Maximum   *float64 `json:"maximum,omitempty"`   // 数值上界
	MinLength *int     `json:"minLength,omitempty"` // 字符串最小长度
	MaxLength *int     `json:"maxLength,omitempty"` // 字符串最大长度
	Pattern   *string  `json:"pattern,omitempty"`   // 正则约束
	Ref       string   `json:"ref,omitempty"`       // 跨主题引用，形如 "topic" 或 "topic:2"
}

// Schema 是一个主题下注册的模式版本内容。
type Schema struct {
	Name      string   `json:"name"`
	Type      Type     `json:"type"`
	Fields    []Field  `json:"fields,omitempty"`
	Items     *Field   `json:"items,omitempty"`
	Enum      []any    `json:"enum,omitempty"`
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
	MinLength *int     `json:"minLength,omitempty"`
	MaxLength *int     `json:"maxLength,omitempty"`
	Pattern   *string  `json:"pattern,omitempty"`
	Ref       string   `json:"ref,omitempty"`
}

// ValidationError 描述一个校验错误，Path 为出错字段路径。
type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidateResult 汇总全部校验错误。
type ValidateResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors"`
}

// Compatibility 兼容性级别。
type Compatibility string

const (
	CompatibilityNone     Compatibility = "NONE"
	CompatibilityBackward Compatibility = "BACKWARD"
	CompatibilityForward  Compatibility = "FORWARD"
	CompatibilityFull     Compatibility = "FULL"
)

// CompatResult 兼容性判定结果。
type CompatResult struct {
	Compatible bool     `json:"compatible"`
	Errors     []string `json:"errors,omitempty"`
}
