package postgres_test

import (
	"testing"

	"app.local/internal/platform/postgres"
)

func TestContainsPattern(t *testing.T) {
	for in, want := range map[string]string{
		"键盘":   "%键盘%",
		"50%":  `%50\%%`,
		"a_b":  `%a\_b%`,
		`c:\d`: `%c:\\d%`,
	} {
		if got := postgres.ContainsPattern(in); got != want {
			t.Errorf("ContainsPattern(%q) = %q, want %q", in, got, want)
		}
	}
}
