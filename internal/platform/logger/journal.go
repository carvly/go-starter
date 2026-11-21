package logger

import (
	"bytes"
	"io"
)

// priorityWriter 在每行前加 syslog 优先级前缀（<3> 之类）：journald 默认 SyslogLevelPrefix=yes，据此设置
// PRIORITY 并去掉前缀，journalctl -p err 才能按级别过滤。JSONHandler 每条记录只调一次 Write，
// 内容以 {"time":...,"level":"..." 开头，第一个 "level" 就是记录的级别。
type priorityWriter struct{ w io.Writer }

var levelKey = []byte(`"level":"`)

func (p priorityWriter) Write(b []byte) (int, error) {
	prefix := priorityOf(b)
	buf := make([]byte, 0, len(prefix)+len(b))
	buf = append(append(buf, prefix...), b...)
	if _, err := p.w.Write(buf); err != nil {
		return 0, err
	}
	return len(b), nil
}

func priorityOf(b []byte) string {
	i := bytes.Index(b, levelKey)
	if i < 0 {
		return "<6>"
	}
	level := b[i+len(levelKey):]
	switch {
	case bytes.HasPrefix(level, []byte("ERROR")):
		return "<3>"
	case bytes.HasPrefix(level, []byte("WARN")):
		return "<4>"
	case bytes.HasPrefix(level, []byte("DEBUG")):
		return "<7>"
	default:
		return "<6>"
	}
}
