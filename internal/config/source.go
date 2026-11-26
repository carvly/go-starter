package config

import (
	"fmt"
	"os"
	"slices"
	"time"

	"go.yaml.in/yaml/v4"
)

// Source 是严格解析后的 CONFIG_FILE。各配置拥有方认领自己的顶层段，Finish
// 检查是否还有未认领段。
type Source struct {
	sections map[string]yaml.Node
	claimed  map[string]bool
}

func NewSource() *Source {
	return &Source{sections: map[string]yaml.Node{}, claimed: map[string]bool{}}
}

func loadSource() (*Source, error) {
	src := NewSource()
	path := os.Getenv("CONFIG_FILE")
	if path == "" {
		return src, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: CONFIG_FILE=%q is set but unreadable: %w", path, err)
	}
	if err := yaml.Load(data, &src.sections, yaml.WithUniqueKeys()); err != nil {
		return nil, fmt.Errorf("config: parse CONFIG_FILE=%q: %w", path, err)
	}
	if src.sections == nil {
		return nil, fmt.Errorf("config: parse CONFIG_FILE=%q: empty document", path)
	}
	return src, nil
}

// Section 把 name 段解码到已填好默认值的目标上；段不存在时保留默认值。
func (s *Source) Section(name string, out any) error {
	if s == nil {
		return nil
	}
	s.claimed[name] = true
	node, ok := s.sections[name]
	if !ok {
		return nil
	}
	data, err := yaml.Dump(&node)
	if err != nil {
		return fmt.Errorf("config: encode section %s: %w", name, err)
	}
	if err := yaml.Load(data, out, yaml.WithKnownFields(), yaml.WithUniqueKeys()); err != nil {
		return fmt.Errorf("config: parse section %s: %w", name, err)
	}
	return nil
}

// Env 用环境变量覆盖一个已解码字段。
func (s *Source) Env(name string, dst any) error {
	switch v := dst.(type) {
	case *string:
		setString(v, name)
	case *[]string:
		setStringSlice(v, name)
	case *map[string]string:
		return setStringMap(v, name)
	case *bool:
		return setBool(v, name)
	case *int32:
		return setInt32(v, name)
	case *int64:
		return setInt64(v, name)
	case *uint32:
		return setUint32(v, name)
	case *uint8:
		return setUint8(v, name)
	case *float64:
		return setFloat64(v, name)
	case *time.Duration:
		return setDuration(v, name)
	default:
		return fmt.Errorf("config: unsupported env destination %T for %s", dst, name)
	}
	return nil
}

func (s *Source) Finish() error {
	if s == nil {
		return nil
	}
	var unknown []string
	for name := range s.sections {
		if !s.claimed[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return fmt.Errorf("config: unclaimed top-level section(s): %v", unknown)
	}
	return nil
}
