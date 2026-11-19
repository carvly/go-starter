package domain_test

import (
	"testing"
	"time"

	"app.local/internal/modules/staff/domain"
)

func TestSessionStatus(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	idle := 30 * time.Minute
	revokedAt := now.Add(-time.Hour)
	tests := []struct {
		name string
		s    domain.Session
		want domain.SessionStatus
	}{
		{"有效", domain.Session{LastUsedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}, domain.SessionActive},
		{"已吊销优先于过期", domain.Session{LastUsedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour), RevokedAt: &revokedAt}, domain.SessionRevoked},
		{"超过绝对有效期", domain.Session{LastUsedAt: now.Add(-time.Minute), ExpiresAt: now}, domain.SessionExpired},
		{"空闲超时", domain.Session{LastUsedAt: now.Add(-idle), ExpiresAt: now.Add(time.Hour)}, domain.SessionExpired},
	}
	for _, tt := range tests {
		if got := tt.s.Status(now, idle); got != tt.want {
			t.Errorf("%s: Status = %q, want %q", tt.name, got, tt.want)
		}
	}
}
