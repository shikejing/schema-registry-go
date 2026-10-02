package schema

import (
	"fmt"
)

// CheckCompatibility 判定新模式相对旧模式的兼容性。
//
// 语义说明：
//   - BACKWARD（向后兼容）：用新模式写出的消费者可以读取旧模式产生的数据。
//     即新模式不能新增必填字段、不能收紧约束（枚举/范围/长度）、不能改类型。
//   - FORWARD（向前兼容）：用旧模式写出的消费者可以读取新模式产生的数据。
//     即新模式不能删除必填字段、不能放宽约束、不能改类型。
//   - FULL（完整兼容）：同时满足向后与向前兼容。
//   - NONE：不做兼容性检查。
func CheckCompatibility(newS, oldS *Schema, mode Compatibility, resolver Resolver) *CompatResult {
	res := &CompatResult{Compatible: true}
	if mode == CompatibilityNone {
		return res
	}
	if newS == nil || oldS == nil {
		res.Compatible = false
		res.Errors = append(res.Errors, "新模式与旧模式均不能为空")
		return res
	}
	errs := []string{}
	compareFields(fieldFromSchema(newS), fieldFromSchema(oldS), mode, resolver, map[string]bool{}, map[string]bool{}, "$", &errs)
	if len(errs) > 0 {
		res.Compatible = false
		res.Errors = errs
	}
	return res
}

func compareFields(newF, oldF Field, mode Compatibility, resolver Resolver, visitedNew, visitedOld map[string]bool, path string, errs *[]string) {
	// 引用字段：解析后按被引用模式比较（引用自身在注册时会被循环校验拒绝）。
	if newF.Ref != "" {
		if resolved, err := resolveRefField(newF, resolver, visitedNew); err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: %v", path, err))
			return
		} else if resolved != nil {
			resolved.Name = newF.Name
			resolved.Required = newF.Required
			newF = *resolved
		}
	}
	if oldF.Ref != "" {
		if resolved, err := resolveRefField(oldF, resolver, visitedOld); err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: %v", path, err))
			return
		} else if resolved != nil {
			resolved.Name = oldF.Name
			resolved.Required = oldF.Required
			oldF = *resolved
		}
	}

	if newF.Type != oldF.Type {
		*errs = append(*errs, fmt.Sprintf("%s: 类型由 %q 变为 %q，不兼容", path, oldF.Type, newF.Type))
		return
	}

	switch newF.Type {
	case TypeObject:
		compareObject(newF, oldF, mode, resolver, visitedNew, visitedOld, path, errs)
	case TypeArray:
		if newF.Items != nil && oldF.Items != nil {
			compareFields(*newF.Items, *oldF.Items, mode, resolver, visitedNew, visitedOld, path+"[]", errs)
		}
		compareLength(newF.MinLength, oldF.MinLength, newF.MaxLength, oldF.MaxLength, mode, path, errs)
	case TypeString:
		compareLength(newF.MinLength, oldF.MinLength, newF.MaxLength, oldF.MaxLength, mode, path, errs)
		comparePattern(newF.Pattern, oldF.Pattern, path, errs)
	case TypeInteger, TypeNumber:
		compareBounds(newF.Minimum, oldF.Minimum, newF.Maximum, oldF.Maximum, mode, path, errs)
		compareEnum(newF.Enum, oldF.Enum, mode, path, errs)
	case TypeBoolean:
		compareEnum(newF.Enum, oldF.Enum, mode, path, errs)
	}
}

func resolveRefField(f Field, resolver Resolver, visited map[string]bool) (*Field, error) {
	if f.Ref == "" {
		return &f, nil
	}
	if visited[f.Ref] {
		// 引用环：跳过展开，循环引用由注册阶段的引用校验拒绝。
		return nil, nil
	}
	visited[f.Ref] = true
	if resolver == nil {
		return nil, fmt.Errorf("引用 %q 无法解析（缺少解析器）", f.Ref)
	}
	schema, err := resolver(f.Ref)
	if err != nil {
		return nil, fmt.Errorf("引用 %q 解析失败: %v", f.Ref, err)
	}
	resolved := fieldFromSchema(schema)
	return &resolved, nil
}

func compareObject(newF, oldF Field, mode Compatibility, resolver Resolver, visitedNew, visitedOld map[string]bool, path string, errs *[]string) {
	oldMap := map[string]Field{}
	for _, f := range oldF.Fields {
		oldMap[f.Name] = f
	}
	newMap := map[string]Field{}
	for _, f := range newF.Fields {
		newMap[f.Name] = f
	}

	switch mode {
	case CompatibilityBackward:
		// 新增必填字段不允许；删除字段允许；共有字段递归比较。
		for _, nf := range newF.Fields {
			of, exists := oldMap[nf.Name]
			if !exists {
				if nf.Required {
					*errs = append(*errs, fmt.Sprintf("%s: 向后兼容不允许新增必填字段 %q", path, nf.Name))
				}
				continue
			}
			if nf.Required && !of.Required {
				*errs = append(*errs, fmt.Sprintf("%s: 向后兼容不允许字段 %q 由可选变为必填", path, nf.Name))
			}
			compareFields(nf, of, mode, resolver, visitedNew, visitedOld, joinPath(path, nf.Name), errs)
		}
	case CompatibilityForward:
		// 删除必填字段不允许；新增字段允许；共有字段递归比较。
		for _, of := range oldF.Fields {
			nf, exists := newMap[of.Name]
			if !exists {
				if of.Required {
					*errs = append(*errs, fmt.Sprintf("%s: 向前兼容不允许删除必填字段 %q", path, of.Name))
				}
				continue
			}
			if !nf.Required && of.Required {
				*errs = append(*errs, fmt.Sprintf("%s: 向前兼容不允许字段 %q 由必填变为可选", path, of.Name))
			}
			compareFields(nf, of, mode, resolver, visitedNew, visitedOld, joinPath(path, of.Name), errs)
		}
	case CompatibilityFull:
		// 完整兼容：新增/删除字段均不允许，共有字段递归比较。
		for _, nf := range newF.Fields {
			of, exists := oldMap[nf.Name]
			if !exists {
				*errs = append(*errs, fmt.Sprintf("%s: 完整兼容不允许新增字段 %q", path, nf.Name))
				continue
			}
			if nf.Required != of.Required {
				*errs = append(*errs, fmt.Sprintf("%s: 完整兼容不允许字段 %q 的必填性发生变化", path, nf.Name))
			}
			compareFields(nf, of, mode, resolver, visitedNew, visitedOld, joinPath(path, nf.Name), errs)
		}
		for _, of := range oldF.Fields {
			if _, exists := newMap[of.Name]; !exists {
				*errs = append(*errs, fmt.Sprintf("%s: 完整兼容不允许删除字段 %q", path, of.Name))
			}
		}
	}
}

// compareBounds 数值范围：
//   - 向后兼容要求新模式范围是旧模式的超集（newMin≤oldMin，newMax≥oldMax）；
//   - 向前兼容要求新模式范围是旧模式的子集（newMin≥oldMin，newMax≤oldMax）。
func compareBounds(newMin, oldMin, newMax, oldMax *float64, mode Compatibility, path string, errs *[]string) {
	switch mode {
	case CompatibilityBackward:
		if newMin != nil && (oldMin == nil || *newMin > *oldMin) {
			*errs = append(*errs, fmt.Sprintf("%s: 向后兼容不允许收紧 minimum（%v -> %v）", path, ptrStr(oldMin), *newMin))
		}
		if newMax != nil && (oldMax == nil || *newMax < *oldMax) {
			*errs = append(*errs, fmt.Sprintf("%s: 向后兼容不允许收紧 maximum（%v -> %v）", path, ptrStr(oldMax), *newMax))
		}
	case CompatibilityForward:
		if oldMin != nil && (newMin == nil || *newMin < *oldMin) {
			*errs = append(*errs, fmt.Sprintf("%s: 向前兼容不允许放宽 minimum（%v -> %v）", path, *oldMin, ptrStr(newMin)))
		}
		if oldMax != nil && (newMax == nil || *newMax > *oldMax) {
			*errs = append(*errs, fmt.Sprintf("%s: 向前兼容不允许放宽 maximum（%v -> %v）", path, *oldMax, ptrStr(newMax)))
		}
	case CompatibilityFull:
		compareBounds(newMin, oldMin, newMax, oldMax, CompatibilityBackward, path, errs)
		compareBounds(newMin, oldMin, newMax, oldMax, CompatibilityForward, path, errs)
	}
}

// compareLength 字符串/数组长度约束，规则与数值范围一致。
func compareLength(newMin, oldMin, newMax, oldMax *int, mode Compatibility, path string, errs *[]string) {
	switch mode {
	case CompatibilityBackward:
		if newMin != nil && (oldMin == nil || *newMin > *oldMin) {
			*errs = append(*errs, fmt.Sprintf("%s: 向后兼容不允许收紧 minLength", path))
		}
		if newMax != nil && (oldMax == nil || *newMax < *oldMax) {
			*errs = append(*errs, fmt.Sprintf("%s: 向后兼容不允许收紧 maxLength", path))
		}
	case CompatibilityForward:
		if oldMin != nil && (newMin == nil || *newMin < *oldMin) {
			*errs = append(*errs, fmt.Sprintf("%s: 向前兼容不允许放宽 minLength", path))
		}
		if oldMax != nil && (newMax == nil || *newMax > *oldMax) {
			*errs = append(*errs, fmt.Sprintf("%s: 向前兼容不允许放宽 maxLength", path))
		}
	case CompatibilityFull:
		compareLength(newMin, oldMin, newMax, oldMax, CompatibilityBackward, path, errs)
		compareLength(newMin, oldMin, newMax, oldMax, CompatibilityForward, path, errs)
	}
}

// compareEnum 枚举：
//   - 向后兼容要求新枚举包含旧枚举的全部值；
//   - 向前兼容要求新枚举的全部值都包含在旧枚举中。
func compareEnum(newEnum, oldEnum []any, mode Compatibility, path string, errs *[]string) {
	switch mode {
	case CompatibilityBackward:
		for _, ov := range oldEnum {
			if !containsValue(newEnum, ov) {
				*errs = append(*errs, fmt.Sprintf("%s: 向后兼容不允许移除枚举值 %v", path, ov))
			}
		}
	case CompatibilityForward:
		for _, nv := range newEnum {
			if !containsValue(oldEnum, nv) {
				*errs = append(*errs, fmt.Sprintf("%s: 向前兼容不允许新增枚举值 %v", path, nv))
			}
		}
	case CompatibilityFull:
		compareEnum(newEnum, oldEnum, CompatibilityBackward, path, errs)
		compareEnum(newEnum, oldEnum, CompatibilityForward, path, errs)
	}
}

// comparePattern 模式变更无法静态证明存量数据仍然匹配，任何差异均判定为不兼容（保守策略）。
func comparePattern(newP, oldP *string, path string, errs *[]string) {
	diff := false
	switch {
	case newP == nil && oldP == nil:
		diff = false
	case newP == nil || oldP == nil:
		diff = true
	default:
		diff = *newP != *oldP
	}
	if diff {
		*errs = append(*errs, fmt.Sprintf("%s: pattern 发生变化，无法证明存量数据仍满足，判定为不兼容", path))
	}
}

func containsValue(enum []any, target any) bool {
	for _, v := range enum {
		if valuesEqual(v, target) {
			return true
		}
	}
	return false
}

func ptrStr[T any](p *T) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("%v", *p)
}
