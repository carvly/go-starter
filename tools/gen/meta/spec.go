package meta

import (
	"fmt"

	"go.yaml.in/yaml/v4"
)

// 只解析生成 meta.gen.ts 用到的那部分 OpenAPI。用 yaml 而不是 kin-openapi：输出要保持 spec 里
// 字段的书写顺序，kin-openapi 把 properties 解析成 map，顺序就丢了。

type entry[T any] struct {
	key string
	val T
}

// ordered 是保留书写顺序的 YAML 映射。
type ordered[T any] []entry[T]

func (o *ordered[T]) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("第 %d 行：应该是键值映射", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		var v T
		if err := n.Content[i+1].Decode(&v); err != nil {
			return err
		}
		*o = append(*o, entry[T]{key: n.Content[i].Value, val: v})
	}
	return nil
}

type schema struct {
	Ref        string           `yaml:"$ref"`
	Type       string           `yaml:"type"`
	Format     string           `yaml:"format"`
	Title      string           `yaml:"title"`
	Enum       []string         `yaml:"enum"`
	EnumLabels ordered[string]  `yaml:"x-enum-labels"`
	Field      string           `yaml:"x-field"`
	Required   []string         `yaml:"required"`
	Properties ordered[*schema] `yaml:"properties"`
	Items      *schema          `yaml:"items"`
	AllOf      []*schema        `yaml:"allOf"`
	MinLength  *int64           `yaml:"minLength"`
	MaxLength  *int64           `yaml:"maxLength"`
	Minimum    *int64           `yaml:"minimum"`
	Maximum    *int64           `yaml:"maximum"`
	Default    any              `yaml:"default"`
}

type parameter struct {
	Ref    string  `yaml:"$ref"`
	Name   string  `yaml:"name"`
	In     string  `yaml:"in"`
	Schema *schema `yaml:"schema"`
}

type operation struct {
	OperationID string       `yaml:"operationId"`
	Parameters  []*parameter `yaml:"parameters"`
	RequestBody *struct {
		Content map[string]struct {
			Schema *schema `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"requestBody"`
}

type document struct {
	Paths      ordered[ordered[yaml.Node]] `yaml:"paths"`
	Components struct {
		Schemas    ordered[*schema]      `yaml:"schemas"`
		Parameters map[string]*parameter `yaml:"parameters"`
	} `yaml:"components"`
}
