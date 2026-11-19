package staff

import "app.local/internal/modules/staff/jobs"

func JobKinds() []string {
	return []string{jobs.AuditCleanupArgs{}.Kind()}
}
