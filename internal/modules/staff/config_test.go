package staff

import (
	"strings"
	"testing"
	"time"

	"app.local/internal/config"
)

func TestLoadConfig_DefaultsAndValidation(t *testing.T) {
	cfg, err := LoadConfig(config.NewSource())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionIdleTimeout != 30*time.Minute || cfg.SessionMaxAge != 12*time.Hour || cfg.TempPasswordTTL != 24*time.Hour || cfg.AuditRetention != 180*24*time.Hour {
		t.Errorf("defaults = %+v", cfg)
	}
	for _, env := range []string{"STAFF_SESSION_IDLE_TIMEOUT", "STAFF_SESSION_MAX_AGE", "STAFF_TEMP_PASSWORD_TTL", "STAFF_AUDIT_RETENTION"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "0s")
			if _, err := LoadConfig(config.NewSource()); err == nil || !strings.Contains(err.Error(), env) {
				t.Errorf("%s=0 error = %v", env, err)
			}
		})
	}
}
