package postgres

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
	"uuid"
)

// EncodeCursor 编码"最后一条记录的 created_at + id"。用 cursor 而不是 offset，
// 避免大偏移量查询退化；上层只把它当不透明字符串传递。
func EncodeCursor(createdAt time.Time, id string) string {
	raw := fmt.Sprintf("%d|%s", createdAt.UnixNano(), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor 解析 EncodeCursor 生成的 cursor。cursor 为空表示第一页，
// 返回 ok=false。cursor 来自客户端，id 不是合法 UUID 时直接报错：否则会在
// 数据库里和 uuid 列比较时报 22P02，变成 500。
func DecodeCursor(cursor string) (createdAt time.Time, id string, ok bool, err error) {
	if cursor == "" {
		return time.Time{}, "", false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", false, fmt.Errorf("postgres: invalid cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", false, fmt.Errorf("postgres: invalid cursor format")
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, "", false, fmt.Errorf("postgres: invalid cursor timestamp: %w", err)
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return time.Time{}, "", false, fmt.Errorf("postgres: invalid cursor id: %w", err)
	}
	return time.Unix(0, nanos), parts[1], true, nil
}
