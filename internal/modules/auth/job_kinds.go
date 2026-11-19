package auth

import (
	"app.local/internal/modules/auth/events"
	"app.local/internal/modules/auth/jobs"
)

func JobKinds() []string {
	return []string{jobs.CleanupArgs{}.Kind(), events.UserDeleted{}.Kind()}
}
