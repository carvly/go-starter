package resource

import (
	"encoding/json"
	"reflect"
	"strings"
)

// JSONSchema 从 Descriptor 的 Go 类型导出描述文件的 JSON Schema（派生，不手写）。它只覆盖
// 结构（键名、类型）；跨键的语义规则只有 gen check 能查，Schema 通过不代表描述文件正确。
func JSONSchema() ([]byte, error) {
	s := schemaOf(reflect.TypeFor[Descriptor]())
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	s["title"] = "资源描述文件（docs/agents/generator.md）"
	return json.MarshalIndent(s, "", "  ")
}

func schemaOf(t reflect.Type) map[string]any {
	switch t {
	case rawType:
		return map[string]any{"type": []string{"string", "number", "boolean"}}
	case oneOrMoreType:
		return map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return schemaOf(t.Elem())
	case reflect.Struct:
		props := map[string]any{}
		for i := range t.NumField() {
			f := t.Field(i)
			p := schemaOf(f.Type)
			if d := f.Tag.Get("desc"); d != "" {
				p["description"] = d
			}
			props[f.Tag.Get("yaml")] = p
		}
		return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	case reflect.Slice:
		e := t.Elem()
		if e.Kind() == reflect.Struct && e.NumField() == 2 && e.Field(0).Name == "Key" {
			return map[string]any{"type": "object", "additionalProperties": schemaOf(e.Field(1).Type)}
		}
		return map[string]any{"type": "array", "items": schemaOf(e)}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int64:
		return map[string]any{"type": "integer"}
	}
	panic("resource: unsupported type " + strings.ToLower(t.Kind().String()))
}
